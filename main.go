package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"

	"metis-agent/internal/acp"
	"metis-agent/internal/agent"
	"metis-agent/internal/checkpoint"
	"metis-agent/internal/config"
	"metis-agent/internal/doctor"
	"metis-agent/internal/llm"
	"metis-agent/internal/mcp"
	"metis-agent/internal/session"
	"metis-agent/internal/skills"
	"metis-agent/internal/tools"
	"metis-agent/internal/tui"
	"metis-agent/internal/update"
)

// version is set at build time with -ldflags "-X main.version=<tag>".
var version = "dev"

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
- web_search: search the public web for current information
- download_file: download a URL to a file
- repo_map: compact outline of functions, types, and classes in the repository
- semantic_search: search the repository by meaning when an embedding model is configured
- diagnostics: run the project's build, type-check, and lint commands
- subagent: delegate an independent read-only investigation to a fresh agent
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
1. Explore before editing. Start with project_info on unfamiliar repositories, then use repo_map, list_dir, glob, search, and file_read.
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

const helpText = `usage: metis-agent [flags] [--once "task"]
  --provider NAME     provider preset: auto, llama-prism, lmstudio, ollama,
                      openai, openrouter, groq, together, compatible
  --base-url URL      OpenAI-compatible base URL (overrides the preset)
  --api-key KEY       API key (prefer the provider's environment variable)
  --model NAME        model name (auto-detected when a provider has one model)
  --port PORT         legacy shortcut: local server port
  --temp T            temperature (default: 0.3)
  --thinking LEVEL    thinking level (none|low|medium|high|xhigh, default: none)
  --max-steps N       max agent steps (default: 40)
  --window N          conversation messages kept in context (default: 64)
  --system PROMPT     system prompt override
  --dir DIR           project working dir (default: ".")
  --no-shell          disable the shell tool
  --shell-timeout N   shell timeout in seconds (default: 120)
  --auto-approve, -y  run dangerous tool calls without asking
                      (default: ask before shell, deletes, outside-project access,
                      bulk edits, downloads, and non-GET HTTP)
  --approve-edits     review in-project file edits with a diff before writing
  --redact-secrets    redact credentials in tool output (default: on)
  --no-redact         disable credential redaction
  --verify CMD        run CMD automatically after a turn changes files
  --verify-timeout N  auto-verify timeout in seconds (default: 120)
  --fallback-models L comma-separated fallback models
  --instructions L    comma-separated extra instruction files
  --embedding-model M embedding model for semantic_search (for example nomic-embed-text)
  --highlight MODE    syntax highlighting: auto, dark, light, off
  --config PATH       load this config file instead of the standard ones
  --no-config         ignore config files (environment and flags still apply)
  --init-config       write a starter config file and exit
  --list-models       list models from the configured/reachable providers
  --session NAME      load or create a named session
  --resume            resume the latest session for this directory
  --no-session        disable session persistence
  --plain             disable the TUI and use the line REPL
  --json              stream NDJSON events on stdout (for editors)
  --acp               run as an Agent Client Protocol server over stdio
  --once TASK         single-shot task
  --doctor            diagnose the installation and model backend
  --update            download and install the latest release
  --check-update      report whether a newer release exists
  --undo              restore files changed by the previous turn
  --version           print the version and exit
  -h, --help          show this help
examples:
  metis-agent                        # full-screen TUI, auto-detected provider
  metis-agent --provider ollama      # use Ollama on its default port
  metis-agent --list-models          # discover local models
  metis-agent --auto-approve         # skip dangerous-action confirmations
  metis-agent --plain                # line-based REPL
  metis-agent --once "What time is it?"
  metis-agent --no-shell             # disable command execution
`

func main() {
	provider := flag.String("provider", "", "provider preset (auto, llama-prism, lmstudio, ollama, openai, openrouter, groq, together, compatible)")
	baseURL := flag.String("base-url", "", "OpenAI-compatible base URL")
	apiKey := flag.String("api-key", "", "API key")
	model := flag.String("model", "", "model name")
	port := flag.Int("port", 0, "local server port (legacy shortcut)")
	temp := flag.Float64("temp", -1, "temperature")
	maxSteps := flag.Int("max-steps", 0, "max agent steps")
	window := flag.Int("window", 0, "conversation messages kept in context")
	system := flag.String("system", "", "system prompt override")
	dir := flag.String("dir", ".", "project working dir")
	noShell := flag.Bool("no-shell", false, "disable the shell tool")
	shellTimeout := flag.Int("shell-timeout", 0, "shell timeout in seconds")
	autoApprove := flag.Bool("auto-approve", false, "run dangerous tool calls without asking")
	yesShort := flag.Bool("y", false, "alias for --auto-approve")
	yesLong := flag.Bool("yes", false, "alias for --auto-approve")
	highlight := flag.String("highlight", "", "syntax highlighting: auto, dark, light, off")
	configPath := flag.String("config", "", "load this config file instead of the standard ones")
	noConfig := flag.Bool("no-config", false, "ignore config files")
	initConfig := flag.Bool("init-config", false, "write a starter config file and exit")
	listModels := flag.Bool("list-models", false, "list models and exit")
	sessionName := flag.String("session", "", "load or create a named session")
	resume := flag.Bool("resume", false, "resume the latest session for this directory")
	noSession := flag.Bool("no-session", false, "disable session persistence")
	plain := flag.Bool("plain", false, "disable the TUI and use the line REPL")
	once := flag.String("once", "", "single-shot task")
	jsonMode := flag.Bool("json", false, "stream NDJSON events on stdout (implies --plain)")
	acpMode := flag.Bool("acp", false, "run as an Agent Client Protocol server over stdin/stdout")
	undoFlag := flag.Bool("undo", false, "restore files changed by the previous turn and exit")
	doctorFlag := flag.Bool("doctor", false, "diagnose the installation and model backend")
	updateFlag := flag.Bool("update", false, "download and install the latest release")
	checkUpdate := flag.Bool("check-update", false, "report whether a newer release exists")
	versionFlag := flag.Bool("version", false, "print the version and exit")
	redactSecrets := flag.Bool("redact-secrets", true, "redact credentials in tool output")
	noRedact := flag.Bool("no-redact", false, "do not redact credentials in tool output")
	verifyCmd := flag.String("verify", "", "command to run automatically after a turn changes files")
	verifyTimeout := flag.Int("verify-timeout", 0, "auto-verify timeout in seconds")
	fallbackModels := flag.String("fallback-models", "", "comma-separated fallback models")
	instructionsFlag := flag.String("instructions", "", "comma-separated extra instruction files")
	embeddingModel := flag.String("embedding-model", "", "embedding model for semantic_search (for example nomic-embed-text)")
	approveEdits := flag.Bool("approve-edits", false, "review in-project file edits with a diff before writing")
	thinking := flag.String("thinking", "", "thinking level (none|low|medium|high|xhigh)")
	flag.Usage = func() { fmt.Fprint(os.Stderr, helpText) }
	flag.Parse()

	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })

	if *versionFlag {
		fmt.Printf("metis-agent %s\n", version)
		return
	}
	if *updateFlag {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		res, err := update.Update(ctx, version, os.Stdout)
		if err != nil {
			fatal(err)
		}
		if res.From == res.To {
			fmt.Printf("already up to date (%s)\n", version)
		}
		return
	}
	if *checkUpdate {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rel, err := update.Latest(ctx)
		if err != nil {
			fatal(err)
		}
		if update.IsNewer(version, rel.TagName) {
			fmt.Printf("update available: %s -> %s\n", version, rel.TagName)
			fmt.Println("run: metis-agent --update")
		} else {
			fmt.Printf("up to date (%s)\n", version)
		}
		return
	}

	if *initConfig {
		path := *configPath
		if path == "" {
			var err error
			path, err = config.GlobalPath()
			if err != nil {
				fatal(err)
			}
		}
		if err := config.WriteDefault(path, config.Defaults()); err != nil {
			fatal(err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", path)
		return
	}

	v := config.Defaults()
	if !*noConfig {
		paths := []string{}
		if *configPath != "" {
			paths = append(paths, *configPath)
		} else {
			if gp, err := config.GlobalPath(); err == nil {
				paths = append(paths, gp)
			}
			paths = append(paths, config.ProjectPath(*dir))
		}
		for _, p := range paths {
			f, err := config.LoadFile(p)
			if err != nil {
				if *configPath != "" {
					fatal(fmt.Errorf("config %s: %w", p, err))
				}
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				fmt.Fprintf(os.Stderr, "warning: %v\n", err)
				continue
			}
			f.Apply(&v)
		}
	}
	if err := config.ApplyEnv(&v); err != nil {
		fatal(err)
	}

	if set["provider"] {
		v.Provider = *provider
	}
	if set["base-url"] {
		v.BaseURL = *baseURL
	}
	if set["api-key"] {
		v.APIKey = *apiKey
	}
	if set["model"] {
		v.Model = *model
	}
	if set["port"] {
		v.BaseURL = fmt.Sprintf("http://127.0.0.1:%d", *port)
	}
	if set["temp"] {
		v.Temperature = *temp
	}
	if set["max-steps"] {
		v.MaxSteps = *maxSteps
	}
	if set["window"] {
		v.Window = *window
	}
	if set["shell-timeout"] {
		v.ShellTimeoutSec = *shellTimeout
	}
	if set["auto-approve"] || set["y"] || set["yes"] {
		v.AutoApprove = *autoApprove || *yesShort || *yesLong
	}
	if set["highlight"] {
		v.Highlight = strings.ToLower(*highlight)
	}
	if set["verify"] {
		v.Verify = strings.TrimSpace(*verifyCmd)
	}
	if set["verify-timeout"] {
		v.VerifyTimeoutSec = *verifyTimeout
	}
	if set["fallback-models"] {
		v.FallbackModels = splitCSV(*fallbackModels)
	}
	if set["thinking"] {
		v.Thinking = strings.TrimSpace(*thinking)
	}
	if set["instructions"] {
		v.Instructions = splitCSV(*instructionsFlag)
	}
	if set["embedding-model"] {
		v.EmbeddingModel = strings.TrimSpace(*embeddingModel)
	}
	if set["redact-secrets"] {
		v.RedactSecrets = *redactSecrets
	}
	if *noRedact {
		v.RedactSecrets = false
	}
	if *noShell {
		v.Shell = false
	}
	if *noSession {
		v.Session = false
	}
	if err := v.Validate(); err != nil {
		fatal(err)
	}

	if *undoFlag {
		cp, err := checkpoint.New(*dir)
		if err != nil {
			fatal(err)
		}
		res, err := cp.Undo()
		if err != nil {
			fatal(err)
		}
		fmt.Printf("restored %d path(s) from %s\n", res.Restored, filepath.Base(res.Directory))
		for _, s := range res.Skipped {
			fmt.Fprintf(os.Stderr, "warning: not restored: %s\n", s)
		}
		return
	}

	if *doctorFlag {
		probe := tools.NewRegistryWithOptions(tools.Options{
			WorkDir:     *dir,
			EnableShell: v.Shell,
			AutoApprove: v.AutoApprove,
		})
		checks := doctor.Run(context.Background(), doctor.Options{
			Version:   version,
			Dir:       *dir,
			Values:    v,
			ToolCount: len(probe.List()) + 1,
		})
		failed := 0
		for _, c := range checks {
			mark := "✓"
			if !c.OK {
				mark = "✗"
				failed++
			}
			fmt.Printf("%s %-18s %s\n", mark, c.Name, c.Detail)
		}
		if failed > 0 {
			fmt.Printf("\n%d check(s) need attention\n", failed)
			os.Exit(1)
		}
		fmt.Println("\ninstallation looks healthy")
		return
	}

	if *listModels {
		if err := printModels(context.Background(), v); err != nil {
			fatal(err)
		}
		return
	}

	// Resolve a provider and model. Auto keeps the product portable: it probes
	// the common local servers instead of assuming one particular setup.
	resolveCtx, resolveCancel := context.WithTimeout(context.Background(), 12*time.Second)
	resolved, err := llm.Resolve(resolveCtx, llm.ResolveOptions{
		Provider: v.Provider,
		BaseURL:  v.BaseURL,
		Model:    v.Model,
		APIKey:   v.APIKey,
		Temp:     v.Temperature,
	})
	resolveCancel()
	if err != nil {
		fatal(err)
	}

	if *acpMode {
		ctx, cancel := interruptContext()
		defer cancel()
		factory := func(cwd string) (*agent.Agent, func(), error) {
			built, err := buildAgent(cwd, resolved, v, *system, *approveEdits)
			if err != nil {
				return nil, nil, err
			}
			return built.Agent, built.Close, nil
		}
		err := acp.Serve(ctx, os.Stdin, os.Stdout, acp.Options{
			Version: version,
			WorkDir: *dir,
			Factory: factory,
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			fatal(err)
		}
		return
	}

	built, err := buildAgent(*dir, resolved, v, *system, *approveEdits)
	if err != nil {
		fatal(err)
	}
	defer built.Close()

	a := built.Agent
	router := built.Router
	mgr := built.MCP
	skillsM := built.Skills
	cp := built.Checkpoints
	toolNames := make([]string, 0, len(a.Registry().List()))
	for _, t := range a.Registry().List() {
		toolNames = append(toolNames, t.Name)
	}

	// Sessions are saved automatically in interactive modes. One-shot runs stay
	// ephemeral unless a session was explicitly requested.
	persist := v.Session && !*noSession
	if *once != "" {
		persist = *sessionName != "" || *resume
	}
	if *noSession {
		persist = false
	}
	var (
		store *session.Store
		sess  *session.Session
	)
	if persist {
		store, sess = openSession(*dir, *sessionName, *resume, resolved.Provider, resolved.Model)
		if sess != nil {
			if sess.Provider != "" && sess.Provider != resolved.Provider {
				fmt.Fprintf(os.Stderr, "warning: session used provider %s; now using %s\n", sess.Provider, resolved.Provider)
			}
			if sess.Model != "" && sess.Model != resolved.Model {
				fmt.Fprintf(os.Stderr, "warning: session used model %s; now using %s\n", sess.Model, resolved.Model)
			}
			a.LoadHistory(sess.Messages)
			sess.Provider = resolved.Provider
			sess.Model = resolved.Model
			sess.WorkDir = *dir
		}
	}

	// Single-shot mode never uses the TUI.
	if *once != "" {
		if *jsonMode {
			a.SetEventSink(jsonSink())
		} else {
			a.SetEventSink(plainSink())
		}
		if isatty.IsTerminal(os.Stdin.Fd()) {
			a.SetApprover(stdinApprover(bufio.NewReader(os.Stdin)))
		} else if !v.AutoApprove {
			fmt.Fprintln(os.Stderr, "note: dangerous tools are denied in non-interactive mode; use --auto-approve to allow them")
		}
		ctx, cancel := interruptContext()
		_, runErr := a.Run(ctx, *once)
		cancel()
		if runErr != nil {
			reportErr(runErr)
			os.Exit(1)
		}
		if store != nil && sess != nil {
			if err := saveSession(store, sess, a, resolved.Provider, router.Model(), *dir); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not save session: %v\n", err)
			}
		}
		return
	}

	useTUI := !*plain && !*jsonMode && isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
	if useTUI {
		tui.SetTheme(v.Highlight)
		opts := tui.Options{
			Model:           resolved.Model,
			Server:          resolved.BaseURL,
			Provider:        resolved.Provider,
			Dir:             *dir,
			Tools:           toolNames,
			Highlight:       v.Highlight != "off",
			AutoApprove:     v.AutoApprove,
			SessionsEnabled: store != nil && sess != nil,
			Store:           store,
			Session:         sess,
			MCPManager:      mgr,
			SkillsManager:   skillsM,
			Undo:            undoFunc(cp),
			Commit:          commitFunc(router, *dir),
			ListModels:      listModelNames(router),
			SetModel: func(name string) error {
				if err := router.SetModel(name); err != nil {
					return err
				}
				if sess != nil {
					sess.Model = name
				}
				return nil
			},
			ModelName: router.Model,
		}
		if sess != nil && store != nil {
			opts.SessionID = sess.ID
			opts.SessionPath = store.Path(sess)
		}
		err := tui.Run(a, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	ctx, cancel := interruptContext()
	defer cancel()
	runREPL(ctx, a, replOptions{
		Model:    resolved.Model,
		Provider: resolved.Provider,
		Server:   resolved.BaseURL,
		Dir:      *dir,
		Tools:    toolNames,
		Store:    store,
		Session:  sess,
		MCP:      mgr,
		Skills:   skillsM,
		Router:   router,
		Undo:     undoFunc(cp),
		Commit:   commitFunc(router, *dir),
		JSON:     *jsonMode,
	})
}

// openSession loads or creates the session requested by the command line.
func openSession(dir, name string, resume bool, provider, model string) (*session.Store, *session.Session) {
	store, err := session.DefaultStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: session persistence disabled: %v\n", err)
		return nil, nil
	}
	switch {
	case resume:
		sess, err := store.Latest(dir)
		if err == nil {
			return store, sess
		}
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "warning: could not resume session: %v\n", err)
		} else {
			fmt.Fprintln(os.Stderr, "no previous session for this directory; starting a new one")
		}
		return store, store.New(dir, provider, model)

	case name != "":
		sess, err := store.Load(dir, name)
		if err == nil {
			return store, sess
		}
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "warning: could not load session %q: %v\n", name, err)
			return store, store.New(dir, provider, model)
		}
		sess, err = store.NewNamed(name, dir, provider, model)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
			return store, store.New(dir, provider, model)
		}
		return store, sess

	default:
		return store, store.New(dir, provider, model)
	}
}

func saveSession(store *session.Store, sess *session.Session, a *agent.Agent, provider, model, dir string) error {
	sess.Provider = provider
	sess.Model = model
	sess.WorkDir = dir
	sess.Messages = a.ExportHistory()
	return store.Save(sess)
}

// printModels lists models without requiring a resolved model name.
func printModels(ctx context.Context, v config.Values) error {
	provider := strings.ToLower(strings.TrimSpace(v.Provider))
	if provider == "" {
		provider = "auto"
	}
	if provider == "auto" && v.BaseURL == "" {
		results := llm.ProbeLocal(ctx, v.Temperature, nil)
		any := false
		for _, r := range results {
			if r.Err != nil {
				fmt.Printf("%s (%s): unreachable\n", r.Provider, r.BaseURL)
				continue
			}
			any = true
			fmt.Printf("%s (%s)\n", r.Provider, r.BaseURL)
			for _, m := range r.Models {
				mark := "  "
				if m.Loaded {
					mark = "* "
				}
				status := ""
				if m.Status != "" && m.Status != "available" {
					status = " (" + m.Status + ")"
				}
				fmt.Printf("%s%s%s\n", mark, m.ID, status)
			}
		}
		if !any {
			return fmt.Errorf("no local model server was reachable")
		}
		return nil
	}

	p := provider
	if p == "auto" {
		p = "compatible"
	}
	c, err := llm.NewClientWithOptions(llm.ClientOptions{
		Provider: p,
		BaseURL:  v.BaseURL,
		APIKey:   v.APIKey,
		Temp:     v.Temperature,
	})
	if err != nil {
		return err
	}
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	models, err := c.ListModels(listCtx)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		fmt.Println("(no models returned)")
		return nil
	}
	fmt.Printf("%s (%s)\n", c.Provider, c.BaseURL)
	for _, m := range models {
		mark := "  "
		if m.Loaded {
			mark = "* "
		}
		status := ""
		if m.Status != "" && m.Status != "available" {
			status = " (" + m.Status + ")"
		}
		fmt.Printf("%s%s%s\n", mark, m.ID, status)
	}
	return nil
}

type replOptions struct {
	Model    string
	Provider string
	Server   string
	Dir      string
	Tools    []string
	Store    *session.Store
	Session  *session.Session
	MCP      *mcp.Manager
	Skills   *skills.Manager
	Router   *llm.Router
	Undo     func() (string, error)
	Commit   func(ctx context.Context, message string) (string, error)
	JSON     bool
}

const replCommandHelp = `Commands
  /help              show this help
  /reset             clear the conversation and saved session
  /status            show provider, model, project, and approval mode
  /tools             list the available tools
  /mode [name]       show or switch operating mode (general, planner, builder, debugger, cybersecurity, simplifier)
  /autoapprove [on|off] toggle or set automatic approval
  /approve-edits [on|off] review file edits with a diff first
  /model [name]      list models or switch the active model
  /compact           summarize the conversation to free context
  /context           show context usage
  /undo              restore files changed by the previous turn
  /commit [message]  stage everything and commit (generates a message)
  /mcp [subcommand]  manage MCP servers (list/add/remove/reconnect)
  /skills [subcommand]  manage the skill system (list/info/use/run)
  /quit              leave Metis

The TUI adds /clear, /session, /sessions, /new, /resume, and /expand.
Type / to list commands.`

// stdinApprover asks for confirmation on the terminal. The caller must ensure
// it is reading from an interactive stream and owns the reader.
func stdinApprover(r *bufio.Reader) tools.Approver {
	return func(ctx context.Context, req tools.ApprovalRequest) (tools.ApprovalDecision, error) {
		if err := ctx.Err(); err != nil {
			return tools.ApprovalDeny, err
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "approval required · %s\n", req.Tool)
		if req.Summary != "" {
			fmt.Fprintf(os.Stderr, "  %s\n", oneLine(req.Summary, 400))
		}
		if req.Reason != "" {
			fmt.Fprintf(os.Stderr, "  %s\n", req.Reason)
		}
		fmt.Fprintf(os.Stderr, "  [y] once  [a] always allow %s  [A] auto-approve all  [n] deny: ", req.Tool)
		line, err := r.ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			fmt.Fprintln(os.Stderr)
			return tools.ApprovalDeny, nil
		}
		raw := strings.TrimSpace(line)
		switch {
		case raw == "y" || raw == "Y" || strings.EqualFold(raw, "yes") || strings.EqualFold(raw, "once"):
			return tools.ApprovalOnce, nil
		case raw == "a" || strings.EqualFold(raw, "always"):
			fmt.Fprintf(os.Stderr, "always allowing %s for this session\n", req.Tool)
			return tools.ApprovalAlways, nil
		case raw == "A" || strings.EqualFold(raw, "auto") || strings.EqualFold(raw, "all"):
			fmt.Fprintln(os.Stderr, "auto-approve enabled for this session")
			return tools.ApprovalAll, nil
		default:
			fmt.Fprintln(os.Stderr, "denied")
			return tools.ApprovalDeny, nil
		}
	}
}

// runREPL is the line-based fallback used with --plain or when stdin/stdout
// is not a terminal.
func runREPL(ctx context.Context, a *agent.Agent, opts replOptions) {
	if opts.JSON {
		a.SetEventSink(jsonSink())
	} else {
		a.SetEventSink(plainSink())
	}
	fmt.Fprintf(os.Stderr, "Metis ready. provider %s · model %s · %s\n",
		opts.Provider, opts.Model, opts.Server)
	if opts.Session != nil {
		fmt.Fprintf(os.Stderr, "session %s\n", opts.Session.ID)
	}
	fmt.Fprintln(os.Stderr, "Type a task and press Enter (Ctrl-D to quit). Type / for commands.")

	r := bufio.NewReader(os.Stdin)
	if isatty.IsTerminal(os.Stdin.Fd()) {
		a.SetApprover(stdinApprover(r))
	}
	if a.AutoApprove() {
		fmt.Fprintln(os.Stderr, "approval: auto (dangerous tools run without asking)")
	} else if isatty.IsTerminal(os.Stdin.Fd()) {
		fmt.Fprintln(os.Stderr, "approval: ask (dangerous tools require confirmation)")
	} else {
		fmt.Fprintln(os.Stderr, "approval: dangerous tools are denied (use --auto-approve to allow them)")
	}
	for {
		fmt.Fprint(os.Stderr, "you> ")
		line, err := r.ReadString('\n')
		if err != nil {
			fmt.Println()
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		currentModel := func() string {
			if opts.Router != nil {
				return opts.Router.Model()
			}
			return opts.Model
		}
		if strings.HasPrefix(line, "/") {
			switch strings.Fields(line)[0] {
			case "/quit", "/exit", "/q":
				return
			case "/reset":
				a.Reset()
				if opts.Store != nil && opts.Session != nil {
					opts.Session.Messages = nil
					_ = saveSession(opts.Store, opts.Session, a, opts.Provider, currentModel(), opts.Dir)
				}
				fmt.Fprintln(os.Stderr, "conversation reset")
				continue
			case "/autoapprove", "/auto":
				if len(strings.Fields(line)) > 1 {
					switch strings.ToLower(strings.Fields(line)[1]) {
					case "on", "yes", "true":
						a.SetAutoApprove(true)
					case "off", "no", "false":
						a.SetAutoApprove(false)
					default:
						fmt.Fprintln(os.Stderr, "usage: /autoapprove [on|off]")
						continue
					}
				} else {
					a.SetAutoApprove(!a.AutoApprove())
				}
				if a.AutoApprove() {
					fmt.Fprintln(os.Stderr, "auto-approve enabled")
				} else {
					fmt.Fprintln(os.Stderr, "auto-approve disabled; dangerous tools require confirmation")
				}
				continue
			case "/approve-edits", "/review-edits":
				state := true
				fields := strings.Fields(line)
				if len(fields) > 1 {
					switch strings.ToLower(fields[1]) {
					case "on", "yes", "true":
						state = true
					case "off", "no", "false":
						state = false
					default:
						fmt.Fprintln(os.Stderr, "usage: /approve-edits [on|off]")
						continue
					}
				}
				a.SetApproveEdits(state)
				if state {
					fmt.Fprintln(os.Stderr, "edit review enabled: file edits ask for approval with a diff")
				} else {
					fmt.Fprintln(os.Stderr, "edit review disabled")
				}
				continue
			case "/model":
				fields := strings.Fields(line)
				if len(fields) > 1 {
					if opts.Router == nil {
						fmt.Fprintln(os.Stderr, "model switching is not available")
						continue
					}
					if err := opts.Router.SetModel(fields[1]); err != nil {
						fmt.Fprintf(os.Stderr, "model: %v\n", err)
						continue
					}
					opts.Model = fields[1]
					if opts.Session != nil {
						opts.Session.Model = fields[1]
					}
					fmt.Fprintf(os.Stderr, "model: %s\n", currentModel())
					continue
				}
				if opts.Router == nil {
					fmt.Fprintf(os.Stderr, "current model: %s\n", currentModel())
					continue
				}
				listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				models, err := opts.Router.ListModels(listCtx)
				cancel()
				if err != nil {
					fmt.Fprintf(os.Stderr, "models: %v\n", err)
					continue
				}
				fmt.Fprintf(os.Stderr, "current model: %s\n", currentModel())
				for _, m := range models {
					mark := "  "
					if m.ID == currentModel() {
						mark = "* "
					}
					fmt.Fprintf(os.Stderr, "%s%s\n", mark, m.ID)
				}
				continue
			case "/thinking":
				fields := strings.Fields(line)
				if len(fields) > 1 {
					if opts.Router == nil {
						fmt.Fprintln(os.Stderr, "thinking level switching is not available")
						continue
					}
					lvl, _ := llm.ParseThinkingLevel(fields[1])
                    opts.Router.SetThinking(lvl)
                    fmt.Fprintf(os.Stderr, "thinking: %s\n", lvl.String())
                    continue
				}
				fmt.Fprintf(os.Stderr, "current thinking: %s\n", opts.Router.Thinking().String())
				fmt.Fprint(os.Stderr, "available:  ")
				for i, l := range llm.Levels() {
					if l == opts.Router.Thinking() {
						fmt.Fprintf(os.Stderr, "*%s ", l.String())
					} else {
						fmt.Fprintf(os.Stderr, "%s ", l.String())
					}
					if i < len(llm.Levels())-1 {
						fmt.Fprint(os.Stderr, "  ")
					}
				}
				fmt.Fprintln(os.Stderr)
				fmt.Fprintln(os.Stderr, "change with /thinking <level>")
				continue
			case "/compact":
				compactCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
				summary, err := a.Compact(compactCtx)
				cancel()
				if err != nil {
					fmt.Fprintf(os.Stderr, "compact: %v\n", err)
					continue
				}
				if opts.Store != nil && opts.Session != nil {
					_ = saveSession(opts.Store, opts.Session, a, opts.Provider, currentModel(), opts.Dir)
				}
				fmt.Fprintf(os.Stderr, "conversation compacted\n\n%s\n", summary)
				continue
			case "/context":
				msgs, tokens, window := a.ContextInfo()
				fmt.Fprintf(os.Stderr, "messages  %d (window %d)\napprox    ~%d tokens\nuse /compact to summarize\n", msgs, window, tokens)
				continue
			case "/undo":
				if opts.Undo == nil {
					fmt.Fprintln(os.Stderr, "undo is not available")
					continue
				}
				out, err := opts.Undo()
				if err != nil {
					fmt.Fprintf(os.Stderr, "undo: %v\n", err)
					continue
				}
				fmt.Fprintln(os.Stderr, out)
				continue
			case "/commit":
				if opts.Commit == nil {
					fmt.Fprintln(os.Stderr, "commit is not available")
					continue
				}
				msg := strings.TrimSpace(strings.TrimPrefix(line, strings.Fields(line)[0]))
				commitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
				out, err := opts.Commit(commitCtx, msg)
				cancel()
				if err != nil {
					fmt.Fprintf(os.Stderr, "commit: %v\n", err)
					continue
				}
				fmt.Fprintln(os.Stderr, out)
				continue
			case "/status":
				approval := "ask"
				if a.AutoApprove() {
					approval = "auto"
				}
				fmt.Fprintf(os.Stderr, "provider  %s\nmodel     %s\nserver    %s\nproject   %s\napproval  %s\nmode      %s\ntools     %d\n",
					opts.Provider, currentModel(), opts.Server, opts.Dir, approval, agent.ModeName(a.Mode()), len(opts.Tools))
				continue
			case "/mode":
				fields := strings.Fields(line)
				if len(fields) > 1 {
					m := parseMode(strings.ToLower(fields[1]))
					if m == -1 {
						fmt.Fprintf(os.Stderr, "unknown mode %q. Modes: general, planner, builder, debugger, cybersecurity, simplifier\n", strings.ToLower(fields[1]))
						continue
					}
					a.SetMode(agent.Mode(m))
					fmt.Fprintf(os.Stderr, "mode: %s (%s)\n", agent.ModeName(a.Mode()), agent.ModeDescription(a.Mode()))
				} else {
					mode := a.Mode()
					fmt.Fprintf(os.Stderr, "current mode: %s (%s)\n", agent.ModeName(mode), agent.ModeDescription(mode))
					fmt.Fprintln(os.Stderr, "switch with: /mode <name> (general, planner, builder, debugger, cybersecurity, simplifier)")
				}
				continue
			case "/tools":
				fmt.Fprintf(os.Stderr, "%d tools\n", len(opts.Tools))
				for _, name := range opts.Tools {
					fmt.Fprintf(os.Stderr, "  %s\n", name)
				}
				continue
			case "/mcp":
				if opts.MCP == nil {
					fmt.Fprintln(os.Stderr, "no mcp servers are configured")
				} else {
					fields := strings.Fields(line)
					out := opts.MCP.Command(fields)
					fmt.Fprintln(os.Stderr, out)
				}
				continue
			case "/skills":
				if opts.Skills == nil {
					fmt.Fprintln(os.Stderr, "no skill manager is available")
					continue
				}
				fields := strings.Fields(line)
				if len(fields) >= 3 && strings.ToLower(fields[1]) == "run" {
					name := fields[2]
					cmd, ok := opts.Skills.RunCommand(ctx, []string{name})
					if !ok {
						fmt.Fprintln(os.Stderr, cmd)
						continue
					}
					hj, _ := json.Marshal(map[string]any{"command": cmd})
					fmt.Fprintln(os.Stderr, a.Registry().Execute(ctx, "shell", hj))
					continue
				}
				out := opts.Skills.Command(ctx, fields)
				fmt.Fprintln(os.Stderr, out)
				continue
			case "/help", "/?", "/":
				fmt.Fprintln(os.Stderr, replCommandHelp)
				continue
			default:
				fmt.Fprintf(os.Stderr, "unknown command %q — type / to list commands\n", strings.Fields(line)[0])
				continue
			}
		}
		ctx, cancel := interruptContext()
		_, runErr := a.Run(ctx, line)
		cancel()
		if runErr != nil {
			reportErr(runErr)
			continue
		}
		if opts.Store != nil && opts.Session != nil {
			if err := saveSession(opts.Store, opts.Session, a, opts.Provider, currentModel(), opts.Dir); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not save session: %v\n", err)
			}
		}
		fmt.Println()
	}
}

// plainSink streams assistant text to stdout and tool progress to stderr.
func plainSink() func(agent.Event) {
	wroteText := false
	return func(ev agent.Event) {
		switch ev.Kind {
		case agent.EventText:
			fmt.Print(ev.Text)
			wroteText = true
		case agent.EventToolCall:
			if wroteText {
				fmt.Fprintln(os.Stderr)
				wroteText = false
			}
			if s := summarizeArgs(ev.Tool, ev.Args); s != "" {
				fmt.Fprintf(os.Stderr, "[step %d] %s %s\n", ev.Step, ev.Tool, s)
			} else {
				fmt.Fprintf(os.Stderr, "[step %d] %s\n", ev.Step, ev.Tool)
			}
		case agent.EventToolResult:
			printToolResult(ev)
		case agent.EventDone:
			if !wroteText && ev.Text != "" {
				fmt.Print(ev.Text)
			}
			fmt.Println()
			wroteText = false
		case agent.EventError:
			if wroteText {
				fmt.Fprintln(os.Stderr)
				wroteText = false
			}
		}
	}
}

// summarizeArgs renders the most relevant argument of a tool call for the
// plain REPL.
func summarizeArgs(tool, raw string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return ""
	}
	if cmd, ok := args["command"].(string); ok {
		return "$ " + oneLine(cmd, 200)
	}
	if pat, ok := args["pattern"].(string); ok {
		p, _ := args["path"].(string)
		if p == "" {
			return pat
		}
		return pat + " in " + p
	}
	if from, ok := args["from"].(string); ok {
		to, _ := args["to"].(string)
		return from + " -> " + to
	}
	if u, ok := args["url"].(string); ok {
		return u
	}
	if p, ok := args["path"].(string); ok {
		return p
	}
	return ""
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// parseMode maps a human-friendly mode name to the agent.Mode. Returns -1 when
// the name is unrecognized, so callers can report a helpful error.
func parseMode(name string) int {
	switch strings.ToLower(name) {
	case "", "general", "g":
		return int(agent.ModeGeneral)
	case "planner", "p":
		return int(agent.ModePlanner)
	case "builder", "b":
		return int(agent.ModeBuilder)
	case "debugger", "debug", "d":
		return int(agent.ModeDebugger)
	case "cybersecurity", "cyber", "security", "sec":
		return int(agent.ModeCybersecurity)
	case "simplifier", "simple", "simplify", "simpl":
		return int(agent.ModeSimplifier)
	default:
		return -1
	}
}

// printToolResult shows a short preview of a tool result in the plain REPL.
func printToolResult(ev agent.Event) {
	res := strings.TrimRight(ev.Result, "\n")
	if res == "" {
		return
	}
	if strings.HasPrefix(res, "error:") {
		fmt.Fprintf(os.Stderr, "  %s (%s)\n", oneLine(res, 400), durString(ev.Duration))
		return
	}
	const maxLines = 16
	lines := strings.Split(res, "\n")
	more := 0
	if len(lines) > maxLines {
		more = len(lines) - maxLines
		lines = lines[:maxLines]
	}
	for i, line := range lines {
		line = oneLine(line, 400)
		if i == 0 {
			fmt.Fprintf(os.Stderr, "  ↳ %s (%s)\n", line, durString(ev.Duration))
		} else {
			fmt.Fprintf(os.Stderr, "    %s\n", line)
		}
	}
	if more > 0 {
		fmt.Fprintf(os.Stderr, "    ... (%d more lines; expand in the TUI with Ctrl+O)\n", more)
	}
}

func durString(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	default:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
}

// wireMCPServers connects to the configured MCP servers and registers their
// tools in the registry as external (always-approval) tools. It returns the
// closers to run on exit so stdio servers are torn down cleanly.
// interruptContext returns a context that is cancelled by Ctrl-C/SIGTERM.
func interruptContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		signal.Stop(ch)
		cancel()
	}
}

// reportErr prints a clear, categorized error.
func reportErr(err error) {
	if err == nil {
		return
	}
	if llm.IsModelNotLoaded(err) {
		fmt.Fprintf(os.Stderr, "MODEL NOT LOADED: %v\n", err)
		return
	}
	if e, ok := llm.AsClientError(err); ok {
		fmt.Fprintln(os.Stderr, e)
		return
	}
	fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "metis: %v\n", err)
	os.Exit(1)
}
