// Package agent runs the Metis agent loop: ask the model, execute the tool
// calls it requests, and repeat until it answers or the step budget runs out.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"metis-agent/internal/llm"
	"metis-agent/internal/memory"
	"metis-agent/internal/tools"
)

// ErrMaxSteps is returned when the model keeps calling tools past the budget.
var ErrMaxSteps = errors.New("max steps exceeded")

// Completer is the subset of llm.Client the agent needs. Keeping it an
// interface makes the loop easy to test with a fake model.
type Completer interface {
	Complete(ctx context.Context, messages []llm.Message, tools []llm.Tool, onToken func(string)) (*llm.Completion, error)
}

// EventKind identifies a progress event emitted during a run.
type EventKind int

const (
	// EventStep starts a new model turn.
	EventStep EventKind = iota
	// EventText is a streamed assistant text delta.
	EventText
	// EventToolCall is emitted just before a tool runs.
	EventToolCall
	// EventToolResult is emitted after a tool finishes.
	EventToolResult
	// EventDone is emitted once when the run finishes successfully.
	EventDone
	// EventError is emitted when the run fails.
	EventError
)

// Event is one progress notification from a run. Callbacks are invoked from
// the goroutine running Run, so consumers must not block.
type Event struct {
	Kind     EventKind
	Step     int
	Text     string
	Tool     string
	Args     string
	Result   string
	Err      error
	Duration time.Duration
}

// Mode is the operating posture of Metis. Changing the mode rewrites the
// system prompt used for every model turn, so the agent's behaviour, tone,
// and tool discipline follow the chosen posture. The base prompt (tools +
// workflow + rules) is shared; each mode appends a mode-specific section.
type Mode int

const (
	// ModeGeneral is the all-purpose posture: explore, plan, build, and verify.
	ModeGeneral Mode = iota
	// ModePlanner is dedicated to analysis and planning: inspect the codebase,
	// propose approaches, and leave no code changed unless asked.
	ModePlanner
	// ModeBuilder is focused on implementing and refining code: edit, create,
	// and verify code precisely.
	ModeBuilder
	// ModeDebugger is focused on diagnosing failures: reproduce, isolate, and
	// fix bugs with the fewest invasive changes.
	ModeDebugger
	// ModeCybersecurity is focused on finding and hardening against
	// vulnerabilities: audit code, flag weaknesses, and suggest fixes.
	ModeCybersecurity
	// ModeSimplifier is focused on reducing complexity: simplify code and
	// documentation while preserving behaviour.
	ModeSimplifier
)

// ModeName returns a short human-readable name for m (e.g. "Builder").
func ModeName(m Mode) string {
	names := []string{"General", "Planner", "Builder", "Debugger", "Cybersecurity", "Simplifier"}
	if i := int(m); 0 <= i && i < len(names) {
		return names[i]
	}
	return "General"
}

// ModeDescription returns a one-line description for m, used in the UI.
func ModeDescription(m Mode) string {
	switch m {
	case ModePlanner:
		return "explore, analyze, and plan without changing code"
	case ModeBuilder:
		return "implement, edit, and verify code"
	case ModeDebugger:
		return "diagnose, isolate, and fix bugs"
	case ModeCybersecurity:
		return "find and harden against vulnerabilities"
	case ModeSimplifier:
		return "reduce complexity without changing behaviour"
	default:
		return "general task handling"
	}
}

// baseSystemPrompt is the shared prompt: identity, the tool list, the skill
// workflow, and the non-negotiable rules. Mode-specific sections are appended
// on top by buildSystemPrompt.
const baseSystemPrompt = `You are Metis, a coding agent working inside a project directory. You complete
tasks by using tools, not by guessing.

Tools:
- list_dir: explore the project structure
- glob: find files by pattern (for example **/*.go)
- project_info: detect languages, manifests, git branch, file counts, and top-level layout
- file_read: read a file (use start_line/end_line for large files)
- file_read_many: read several related files in one call
- file_stat: inspect type, size, permissions, modification time, and line count
- search: regex-search file contents
- hash_file: compute or verify an MD5/SHA digest
- json_query: parse JSON files and extract dot/bracket paths
- file_edit: precise replacement in an existing file (preferred for small changes)
- multi_edit: apply several related replacements to one file atomically
- replace_in_files: regex/literal replacement across many files (use dry_run first)
- file_write: create a new file or fully overwrite one
- make_dir: create directories
- file_move: move or rename files and directories
- file_copy: copy a file
- file_delete: delete files or directories
- diff_files: show a unified diff between two files
- git_status: working tree status with branch information
- git_diff: unstaged or staged diffs, optionally for one file
- git_log: recent commits, optionally for a file or all branches
- git_show: show a commit, tag, or object
- git_blame: line-by-line authorship for a file
- http_fetch: make HTTP(S) requests to read docs, APIs, or submit data
- download_file: download a URL to a file
- system_info: OS, architecture, CPU count, user, temp path, and time
- env_get: read environment variables (secrets redacted by default)
- which: locate executables on PATH
- port_check: check whether local TCP ports are open
- todo_write: replace the task list for multi-step work
- todo_read: read the current task list
- shell: run commands such as builds, tests, linters, programs, and package installs
- skills: manage the skill system (list/info/use/run). A skill is a SKILL.md file with a name, description, an optional run command, and a markdown body. Skills live in <project>/.metis-agent/skills/, $METIS_SKILL_PATHS, and ~/.agents/skills/.
- time: current local date and time

Skills:
- When stuck on a task, first call skills {"action":"list"} to see if a skill fits; if it does, call skills {"action":"info","name":...} to read it.
- use loads a skill's body into context so you can consult it for the rest of the task.
- run executes a skill's declared run command. It is approval-gated exactly like shell — the user confirms each run. Do not bypass approval by shelling out to the command directly if a skill's run action is available.

Workflow:
1. Explore before editing. Start with project_info on unfamiliar repositories, then use list_dir, glob, search, and file_read.
2. For multi-step work, write a todo list with todo_write and update it as you go.
3. Make the smallest correct change with file_edit or multi_edit; use file_write for new files. Use replace_in_files for mechanical refactors, previewing with dry_run when the change is broad.
4. Use make_dir, file_move, file_copy, file_delete, and diff_files for filesystem work instead of shell commands when possible.
5. Use the git tools to inspect history and changes rather than shelling out to git.
6. Verify your work with shell: run the build, tests, or linter that applies.
7. If a command fails, read the error and fix it. Iterate until it passes.
8. Finish with a concise summary of what changed and how you verified it.

Rules:
1. Call exactly ONE tool per turn and wait for its result before continuing.
2. Paths may be relative to the project directory or absolute. Prefer staying
   inside the project; operations outside it may require user approval.
3. Dangerous actions (shell commands, deletes, outside-project access, bulk
   edits, downloads, and non-GET HTTP) are confirmed by the harness. If a tool
   returns "approval denied", stop and ask the user how to proceed. Never retry
   a denied call.
4. Prefer non-interactive commands. Never open editors, pagers, or long-running
   servers unless the user explicitly asks.
5. Never claim something works without verifying it when a check is possible.
6. Be concise. Read line ranges instead of dumping huge files.

Your name is Metis. Do not invent tools.`

// modeSections maps a Mode to the extra system-prompt section appended to the
// base prompt. The order in the array must match the Mode constants.
var modeSections = []string{
	// General
	"You operate in general mode: handle the task end-to-end. Explore before you edit, make the smallest correct change, and verify the result with a build, test, or linter where possible.",
	// Planner
	"You operate in PLANNER mode. Your job is to analyze and plan — not to implement. Explore the codebase with project_info, list_dir, glob, search, and file_read. Identify the goal, constraints, risks, and trade-offs. Prefer multiple approaches and recommend the best one with reasoning. Do not modify code, do not run destructive shell commands, and do not call file_write / file_edit / file_move / file_copy / file_delete. End with a clear, actionable plan (numbered steps) the user can approve. Use shell only for read-only, safe, non-mutating checks (e.g. git log, file existence). When the plan is complete, stop and let the user decide the next step.",
	// Builder
	"You operate in BUILDER mode. Your job is to implement and refine code. Use file_edit, multi_edit, file_write, and file_move/copy for precise changes. Make the smallest correct edit; do not rewrite more than necessary. Prefer file tools over shell for filesystem work. After each change, verify with the build, tests, or linter that applies. Keep changes focused and reviewable. Use git tools to inspect current changes (git_status, git_diff) before you finish. If a verification fails, read the error and fix it, then re-verify. Do not ship unverified work.",
	// Debugger
	"You operate in DEBUGGER mode. Your job is to diagnose and fix failures. Reproduce the failure first (run the test or command that fails). Isolate the root cause: read the error, use git log/blame/show to find recent changes, search for the relevant code paths, and verify your hypothesis with targeted observations. Make the smallest change that fixes the root cause — avoid broad rewrites while debugging. Verify the fix with the failing test or command, then run the broader suite to check for regressions. If the fix does not work, revert or adjust and iterate. Keep a brief running account of what you tried and why each step made sense.",
	// Cybersecurity
	"You operate in CYBERSECURITY mode. Your job is to find and harden against vulnerabilities. Audit the code for common issues: injection (SQL, command, path), insecure deserialization, broken authentication and authorization, sensitive data leakage, weak cryptography, SSRF, open redirects, missing input validation, and unsafe file handling. Use search and file_read to locate risky code; do not blindly run untrusted or destructive shell commands. For each finding, report the file, line, and a concrete exploit or data-leak scenario, with severity (low/medium/high/critical) and a recommended fix. Prefer to show a minimal patch via file_edit only when the user explicitly asks; otherwise present the finding and the recommended change for review. Be precise; avoid false positives. Verify any security claim against the actual code before reporting it.",
	// Simplifier
	"You operate in SIMPLIFIER mode. Your job is to reduce code complexity without changing behaviour. Read the target code fully before touching it. Apply well-understood simplifications: remove dead code and unused variables/functions, consolidate duplicates, flatten deep nesting, rename for clarity, and remove needless abstraction. Do NOT remove behaviour, tests, or documentation that encodes intent. After every change, run the existing tests (and build) to confirm behaviour is preserved; if anything breaks, revert the change. Keep the diff small and reviewable. Explain what you removed and why it was safe. Prefer incremental edits (file_edit) over rewrites. Use git diff to double-check your changes before finishing.",
}

// Config wires up everything the agent needs.
type Config struct {
	LLM      Completer
	Registry *tools.Registry
	System   string
	MaxSteps int
	Window   int
	// OnEvent receives progress and streaming updates. Optional.
	OnEvent func(Event)
	// Mode appends a mode-specific section to the system prompt. Defaults to
	// ModeGeneral when unset.
	Mode Mode
}

// Agent holds conversation state across runs.
type Agent struct {
	cfg    Config
	mode   Mode
	hist   *memory.History
	mu     sync.Mutex
}

func New(cfg Config) *Agent {
	if cfg.MaxSteps < 1 {
		cfg.MaxSteps = 40
	}
	if cfg.Window < 1 {
		cfg.Window = 64
	}
	mode := cfg.Mode
	if mode < ModeGeneral || mode > ModeSimplifier {
		mode = ModeGeneral
	}
	return &Agent{cfg: cfg, mode: mode, hist: memory.NewHistory(cfg.Window)}
}

// Reset forgets the conversation history.
func (a *Agent) Reset() {
	a.mu.Lock()
	a.hist.Reset()
	a.mu.Unlock()
}

// ExportHistory returns a snapshot of the full conversation history.
func (a *Agent) ExportHistory() []llm.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hist.All()
}

// LoadHistory replaces the conversation with a saved history. Invalid tool-call
// groups are dropped so a resumed session cannot produce a rejected prompt.
func (a *Agent) LoadHistory(msgs []llm.Message) {
	a.mu.Lock()
	a.hist.Load(msgs)
	a.mu.Unlock()
}

// HistoryLen returns the number of stored messages.
func (a *Agent) HistoryLen() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hist.Len()
}

// SetEventSink replaces the progress callback. Set it before calling Run;
// Run holds the agent lock for its duration.
func (a *Agent) SetEventSink(fn func(Event)) {
	a.mu.Lock()
	a.cfg.OnEvent = fn
	a.mu.Unlock()
}

// System returns the full system prompt: the base prompt with the active mode
// section appended.
func (a *Agent) System() string {
	return buildSystemPrompt(a.cfg.System, a.modeSection())
}

// Mode returns the current mode.
func (a *Agent) Mode() Mode {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mode
}

// SetMode changes the active mode and rebuilds the effective system prompt for
// the next run. The agent lock is taken to keep it safe to call from the TUI.
func (a *Agent) SetMode(m Mode) {
	a.mu.Lock()
	if m < ModeGeneral || m > ModeSimplifier {
		m = ModeGeneral
	}
	a.mode = m
	a.mu.Unlock()
}

// ModeSection returns the mode-specific section appended to the base prompt.
func (a *Agent) modeSection() string {
	if a.mode == ModeGeneral {
		return ""
	}
	return modeSections[a.mode]
}

// Registry returns the agent's tool registry (used by /skills run in the REPL).
func (a *Agent) Registry() *tools.Registry { return a.cfg.Registry }

// SetApprover installs the callback used to confirm dangerous tool calls.
func (a *Agent) SetApprover(fn tools.Approver) { a.cfg.Registry.SetApprover(fn) }

// SetAutoApprove enables or disables automatic approval of dangerous calls.
func (a *Agent) SetAutoApprove(v bool) { a.cfg.Registry.SetAutoApprove(v) }

// AutoApprove reports whether dangerous calls run without confirmation.
func (a *Agent) AutoApprove() bool { return a.cfg.Registry.AutoApprove() }

// Run executes one user task, keeping the conversation across calls. The
// final answer is returned; streamed text and progress are delivered through
// cfg.OnEvent.
func (a *Agent) Run(ctx context.Context, user string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	user = strings.TrimSpace(user)
	if user == "" {
		return "", errors.New("empty task")
	}
	base := a.hist.Len()
	a.hist.Append(llm.Message{Role: "user", Content: user})

	for step := 0; step < a.cfg.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			a.hist.Truncate(base)
			a.emit(Event{Kind: EventError, Step: step + 1, Err: err})
			return "", err
		}
		a.emit(Event{Kind: EventStep, Step: step + 1})

		comp, err := a.cfg.LLM.Complete(ctx, a.buildMessages(), a.cfg.Registry.Specs(), func(s string) {
			a.emit(Event{Kind: EventText, Step: step + 1, Text: s})
		})
		if err != nil {
			a.hist.Truncate(base)
			a.emit(Event{Kind: EventError, Step: step + 1, Err: err})
			return "", err
		}

		if len(comp.ToolCalls) > 0 {
			a.hist.Append(llm.Message{
				Role:      "assistant",
				Content:   comp.Content,
				ToolCalls: comp.ToolCalls,
			})
			for _, call := range comp.ToolCalls {
				if err := ctx.Err(); err != nil {
					a.hist.Truncate(base)
					a.emit(Event{Kind: EventError, Step: step + 1, Err: err})
					return "", err
				}
				name := call.Function.Name
				a.emit(Event{
					Kind: EventToolCall,
					Step: step + 1,
					Tool: name,
					Args: call.Function.Arguments,
				})
				t0 := time.Now()
				result := a.cfg.Registry.Execute(ctx, name, []byte(call.Function.Arguments))
				dur := time.Since(t0)
				a.emit(Event{
					Kind:     EventToolResult,
					Step:     step + 1,
					Tool:     name,
					Result:   result,
					Duration: dur,
				})
				a.hist.Append(llm.Message{Role: "tool", Content: result, ToolCallID: call.ID})
			}
			continue
		}

		if strings.TrimSpace(comp.Content) == "" {
			// Some models occasionally emit an empty turn. Try again rather
			// than returning an empty answer.
			continue
		}

		a.hist.Append(llm.Message{Role: "assistant", Content: comp.Content})
		a.emit(Event{Kind: EventDone, Step: step + 1, Text: comp.Content})
		return comp.Content, nil
	}

	err := fmt.Errorf("%w (%d)", ErrMaxSteps, a.cfg.MaxSteps)
	a.hist.Truncate(base)
	a.emit(Event{Kind: EventError, Step: a.cfg.MaxSteps, Err: err})
	return "", err
}

func (a *Agent) emit(ev Event) {
	if a.cfg.OnEvent != nil {
		a.cfg.OnEvent(ev)
	}
}

// buildMessages returns the effective system prompt followed by the recent history.
func (a *Agent) buildMessages() []llm.Message {
	msgs := a.hist.Messages()
	out := make([]llm.Message, 0, len(msgs)+1)
	out = append(out, llm.Message{Role: "system", Content: buildSystemPrompt(a.cfg.System, a.modeSection())})
	out = append(out, msgs...)
	return out
}

// buildSystemPrompt combines the base prompt with the active mode section.
func buildSystemPrompt(base, mode string) string {
	if mode == "" {
		return base
	}
	return base + "\n\n" + mode
}
