# AGENT — Project Handbook for metis-agent

Read this before touching code. This is the working manual for an agent editing
or extending **metis-agent**. The goal is that any agent can pick up the codebase,
understand the invariants, make a correct change, and verify it — without
reverse-engineering the project from scratch.

> For the user-facing guide (install, run, flags, TUI keys), read `README.md`.
> For a deeper design/security walkthrough, see `.siso/analysis.md`. This file
> is the **working** doc: structure, conventions, invariants, and how to change things safely.

---

## 1. What this project is

**metis-agent** is a local-first coding agent written in **Go 1.25+**. It talks
to any **OpenAI-compatible chat server** (llama-prism, LM Studio, Ollama, OpenAI,
OpenRouter, Groq, Together, or any `--base-url`) via `/v1/chat/completions`,
with **streaming (SSE)** and **tool calling**. Given a task, it explores a project,
edits files precisely, runs builds/tests via shell, inspects git, and iterates
until the task works.

It produces a **single self-contained binary** (`metis-agent`) with three run
modes:

| Mode | Trigger | What it does |
|---|---|---|
| **TUI** | `./metis-agent` (auto when stdin/stdout are terminals) | Full-screen Bubble Tea UI: streaming transcript, tool output, approval prompts, slash commands |
| **Plain REPL** | `--plain` or piped stdin | Line-based `you> ` prompt, tool progress on stderr |
| **One-shot** | `--once "TASK"` | Single task, no TUI, prints answer and exits; ideal for scripting |

---

## 2. Repository structure

```
metis-agent/
├── main.go                     # CLI flags, TUI/REPL/one-shot entry points,
│                               #   provider resolution, session open/save, MCP wiring,
│                               #   version/update/doctor/undo, checkpoints + auto-verify
├── features.go                 # NDJSON event sink, undo/commit/verify helpers,
│                               #   read-only subagent registration
├── agent_build.go              # shared agent construction (CLI + ACP sessions)
├── go.mod                      # module metis-agent; Go 1.25; bubbletea/bubbles/
│                               #   lipgloss + MCP go-sdk
├── go.sum
├── cmd/
│   └── mcp-smoke-server/main.go  # Tiny MCP stdio server (greet/fail tools)
│                                 #   — used ONLY by integration tests
├── internal/
│   ├── agent/                  # The agent loop (see §4)
│   │   ├── agent.go            # Agent, Run(), event emission, history ops,
│   │   │                       #   Compact(), ContextInfo(), auto-verify
│   │   ├── modes.go            # Mode prompts + policyForMode (Planner gate)
│   │   ├── agent_test.go       # fake model tests
│   │   └── modes_test.go       # posture/policy tests
│   ├── acp/                    # Agent Client Protocol server (--acp)
│   │   ├── acp.go              # JSON-RPC over stdio, sessions, permissions
│   │   └── acp_test.go
│   ├── checkpoint/             # Per-turn file snapshots for /undo and --undo
│   │   ├── checkpoint.go       # Manager: Begin/Snapshot/End/Undo, pruning
│   │   └── checkpoint_test.go
│   ├── config/                 # JSON config + env + platform paths
│   │   ├── config.go           # Values, File, Defaults(), ApplyEnv(), Validate()
│   │   └── config_test.go
│   ├── doctor/                 # Installation diagnostics for --doctor
│   ├── embeddings/             # Cached semantic index + OpenAI-compatible client
│   │   ├── embeddings.go       # index build/search, cache, chunking
│   │   └── embeddings_test.go
│   ├── instructions/           # AGENTS.md/METIS.md/CLAUDE.md/rules loading
│   ├── llm/                    # OpenAI-compatible client
│   │   ├── client.go           # SSE parser, JSON fallback, streaming, model-not-loaded
│   │   ├── provider.go         # provider presets, Resolve(), probing, --list-models
│   │   ├── router.go           # runtime model switch + fallback chain
│   │   ├── client_test.go
│   │   ├── provider_test.go
│   │   └── router_test.go
│   ├── memory/                 # Conversation history
│   │   ├── history.go          # sliding window + well-formedness invariant
│   │   └── history_test.go
│   ├── mcp/                    # MCP client + runtime server management
│   │   ├── mcp.go              # ServerConfig, Connect(), ListTools, Call, namespacing
│   │   ├── manager.go          # Manager: /mcp add/remove/reconnect/list,
│   │   │                       #   config persistence + registry reload
│   │   └── manager_test.go
│   ├── redact/                 # Credential redaction for tool output
│   ├── skills/                 # Skill system: discover, load, run SKILL.md files
│   │   ├── skill.go            # Skill type + ParseSkill (frontmatter + body)
│   │   ├── manager.go          # Manager: List/Get/Command/RunCommand, search paths
│   │   └── manager_test.go
│   ├── session/                # Per-project session persistence
│   │   ├── session.go          # Session, Store, atomic Save()
│   │   └── session_test.go
│   ├── update/                 # GitHub release check/download/checksum/replace
│   ├── tools/                  # Tool registry + built-in tools + approval
│   │   ├── registry.go         # Registry, Execute(), approval gating,
│   │   │                       #   mutation hooks/counter, redaction
│   │   ├── approval.go         # needApproval(), ApprovalDecision/Approver, insideProject()
│   │   ├── approval_preview.go # diff previews for edit approvals
│   │   ├── tool.go             # Tool type, schema helpers, path resolvers
│   │   ├── file.go             # list_dir, glob, project_info, file_read(_many),
│   │   │                        #   file_stat, search, hash_file, json_query
│   │   ├── fs.go               # file_edit, multi_edit, replace_in_files, file_write,
│   │   │                        #   make_dir, file_move/copy/delete, diff_files
│   │   ├── git.go              # git_status/diff/log/show/blame
│   │   ├── project.go          # language/manifest/git detection, project_info
│   │   ├── repomap.go          # repo_map symbol outline
│   │   ├── semantic.go         # semantic_search (embedding index)
│   │   ├── diagnostics.go      # diagnostics: detected build/type-check/lint
│   │   ├── websearch.go        # web_search (DuckDuckGo HTML, no API key)
│   │   ├── subagent.go         # subagent tool wrapper (runner injected from main)
│   │   ├── json.go             # json_query
│   │   ├── system.go           # system_info, env_get, which, port_check
│   │   ├── todo.go             # in-session todo store
│   │   ├── web.go              # http_fetch, download_file (+ SSRF guards)
│   │   ├── shell.go            # shell tool: exec, process group kill, output cap, ANSI
│   │   ├── proc_unix.go / proc_other.go   # process-group kill (platform-specific)
│   │   ├── tool_test.go
│   │   └── security_test.go    # path escapes, policy bypass, approval tests
│   └── tui/                    # Bubble Tea UI
│       ├── tui.go              # Model, Update loop, key handling
│       ├── commands.go         # slash commands + completion menu
│       ├── sessions.go         # /session, /sessions, resume & naming
│       ├── approvals.go        # approval prompts, diff previews, decisions
│       ├── view.go             # layout, transcript, header/status/input, context meter
│       ├── styles.go           # palette, transcript rendering
│       ├── highlight.go        # chroma-based syntax highlighting
│       ├── markdown.go         # glamour-based Markdown rendering
│       ├── diff.go             # unified diff rendering
│       └── tui_test.go
├── scripts/
│   ├── install.sh              # curl installer (easy/advanced, --uninstall)
│   └── install.ps1             # native Windows installer
├── .github/workflows/
│   ├── ci.yml                  # fmt, vet, build, race, cross-builds
│   └── release.yml             # tag -> versioned binaries + SHA256SUMS
├── Makefile                    # make check / race / cover
└── LICENSE                     # MIT
```

### Dependency graph (who calls whom)
```
main.go ──> internal/llm      (resolve provider + model, build Router)
           ──> internal/config (load + merge config)
           ──> internal/tools  (build Registry)
           ──> internal/mcp    (connect MCP servers, register external tools)
           ──> internal/session (open/save session)
           ──> internal/checkpoint (per-turn snapshots for undo)
           ──> internal/instructions (AGENTS.md/rules prompt sections)
           ──> internal/embeddings (semantic index, when --embedding-model is set)
           ──> internal/update (--update / --check-update)
           ──> internal/doctor (--doctor)
           ──> internal/agent  (Agent; owns the loop)
                 ──> internal/memory (History)
           ──> internal/acp    (--acp server; builds one Agent per session)
           ──> internal/tui    (TUI/REPL entry)

agent_build.go  ──> the construction above, shared by main and the ACP factory
internal/agent   ──> llm (Complete), tools (Registry.Execute), memory (History)
internal/tools   ──> llm (schema types only), redact (output), embeddings (index)
internal/tui     ──> agent (events), session, tools (approval types)
internal/session ──> llm (Message), config (DataDir)
internal/checkpoint ──> config (DataDir)
internal/instructions ──> config (ConfigDir)
internal/embeddings ──> standard library only (HTTP client + index)
internal/acp     ──> agent, tools (approval types), llm (messages)
internal/doctor  ──> config, checkpoint, instructions, llm, session
internal/mcp     ──> mcp go-sdk
```
**Rule of thumb:** `internal/llm` and `internal/embeddings` are leaves (no
internal deps). `internal/agent` depends on `llm`, `memory`, `tools`.
`internal/tui` and `internal/acp` depend on `agent` and `tools`. Never add a
dependency *up* the graph (e.g. don't import `internal/tools` from
`internal/llm`). `features.go` wires the parent registry, the subagent's
read-only registry, and the router; `agent_build.go` wires a complete agent for
the CLI and for every ACP session.

---

## 3. Configuration & flags (precedence)

**Precedence, later wins:**
1. Global config file (`~/.config/metis-agent/config.json` or platform equivalent)
2. Project config file (`./.metis-agent.json`)
3. Environment variables (`METIS_*`)
4. CLI flags

| Source | Examples |
|---|---|
| Global config | `~/.config/metis-agent/config.json` |
| Project config | `./.metis-agent.json` |
| Env | `METIS_PROVIDER`, `METIS_MODEL`, `METIS_BASE_URL`, `METIS_API_KEY`, `METIS_TEMPERATURE`, `METIS_MAX_STEPS`, `METIS_WINDOW`, `METIS_SHELL_TIMEOUT_SEC`, `METIS_AUTO_APPROVE`, `METIS_REDACT_SECRETS`, `METIS_VERIFY`, `METIS_VERIFY_TIMEOUT_SEC`, `METIS_INSTRUCTIONS`, `METIS_FALLBACK_MODELS`, `METIS_EMBEDDING_MODEL`, `METIS_MCP_SERVERS` |
| Flags | `--provider`, `--model`, `--base-url`, `--temp`, `--thinking`, `--max-steps`, `--window`, `--dir`, `--auto-approve`, `--approve-edits`, `--verify`, `--fallback-models`, `--embedding-model`, `--once`, `--json`, `--acp`, `--doctor`, `--update`, `--undo`, … |

The config file shape (`internal/config/File`) uses **pointers** so an unset field
doesn't override a zero value. `File.Apply(&v)` overlays only non-nil fields.
`METIS_MCP_SERVERS` is a JSON array of `mcp.ServerConfig`, merged over file
servers (same-name entries from env win).

Key flags (defaults): `--temp 0.3`, `--thinking none`, `--max-steps 40`,
`--window 64`, `--shell-timeout 120` (clamped 1–900 in the shell tool), `--dir .`,
`--no-shell`, `--plain`, `--once`, `--session NAME`, `--resume`, `--no-session`,
`--list-models`, `--init-config`, `--highlight auto|dark|light|off`,
`--redact-secrets` (default on; `--no-redact` turns it off),
`--verify-timeout 120`, `--approve-edits`, `--json`, `--acp`,
`--embedding-model NAME`.

Feature-only config fields: `redact_secrets *bool`, `verify *string`,
`verify_timeout_sec *int`, `instructions []string`, `fallback_models []string`,
`thinking *string` (reasoning effort: `none`, `low`, `medium`, `high`, `xhigh`),
`embedding_model *string`. `WriteDefault` writes all of them so `--init-config`
documents the available knobs. `instructions` entries may be absolute or
relative to `--dir`; they are loaded after the conventional global and project
files. `thinking` maps to `llm.ThinkingLevel` and is sent to the provider as
`reasoning_effort` in each chat completion; empty/`none` omits the field so the
server uses its own default. `embedding_model` enables the `semantic_search`
tool; an empty value keeps it out of the tool list entirely.

---

## 4. The agent loop (`internal/agent/agent.go`)

`Agent.Run(ctx, user)` does, per user task:

1. `history.Append(user message)`.
2. Call `BeginTurn` (checkpoint manager starts a new snapshot batch) and reset
   the registry's mutation counter; defer `EndTurn`.
3. Repeat up to `MaxSteps`:
   - send `system prompt + windowed history + tool schemas` with `stream: true`.
   - stream text deltas to the UI via `EventText`.
   - **If tool calls:** append an `assistant` message with `ToolCalls`; run each
     tool **in order** (so cancellation stops a long command); append a `tool`
     message per result; **continue** (another model turn).
   - **If final answer** (no tool calls, non-empty): run `Verify` when
     configured and the turn mutated files; a failure is appended as a new user
     message and the loop continues (at most `MaxVerifyAttempts`, default 2).
     Otherwise append the `assistant` message, emit `EventDone`, return.
4. On `ctx` cancel, `Complete` error, or empty-turn exhaustion: `history.Truncate(base)`
   to roll back the partial step, emit `EventError`, return.

`Compact(ctx)` summarizes the visible transcript with the configured model and
replaces history with a summary/acknowledgement pair. `ContextInfo()` returns
message count, an approximate token count (content + tool arguments / 4), and
the configured window for `/context` and the status meter.

**Key invariants to preserve:**
- **One model turn per step**, and the history is only advanced atomically on
  success — errors roll back to `base`.
- **Event delivery is from the running goroutine.** `OnEvent` callbacks must not
  block (the TUI routes events through Bubble Tea's `p.Send`, which is safe).
- The agent holds `a.mu` for the **entire duration of `Run`** — never re-enters
  or holds other locks. `SetEventSink`/`SetApprover` etc. take the lock too.
  `Compact` also holds `a.mu`, so it cannot run concurrently with `Run`.
- `Completer` is an interface (`Complete(ctx, messages, tools, onToken)`), so the
  loop is testable with a fake model (`internal/agent/agent_test.go`). The real
  implementation in `main.go` is `llm.Router`.

**Events (`agent.Event`):** `EventStep`, `EventText`, `EventToolCall`,
`EventToolResult`, `EventDone`, `EventError`. Each carries `Step`, `Text`,
`Tool`, `Args`, `Result`, `Err`, `Duration`.

### Operating modes

`Mode` (see `internal/agent/agent.go`) is the agent's operating posture. Each
mode appends a **mode-specific section** to the shared base system prompt
(`baseSystemPrompt`) — the base prompt (tool list, skill workflow, and rules)
is identical across modes. Modes are: `ModeGeneral` (default), `ModePlanner`,
`ModeBuilder`, `ModeDebugger`, `ModeCybersecurity`, `ModeSimplifier`.

- `Agent.System()` and `Agent.buildMessages()` use
  `buildSystemPrompt(cfg.System, modeSection())`.
- `SetMode(m Mode)` mutates `a.mode` under `a.mu`. **It must not be called
  while `Run` is executing** (the lock is held for the whole run), or it
  deadlocks. The TUI only switches modes when `m.running` is false.
- Out-of-range modes clamp to `ModeGeneral` (in `New` and `SetMode`).
- **Planner mode is enforced in code, not just the prompt.** `policyForMode` in
  `internal/agent/modes.go` returns a `tools.Policy`; the registry consults it
  before approval, so auto-approve cannot bypass it. In Planner, mutating
  tools, `shell`, non-GET `http_fetch`, external MCP tools, and the skills
  `run` action are refused and hidden from `Registry.Specs()`. The allowlist is
  `plannerReadOnly`; a new tool stays out of Planner until added there.
- Helpers: `ModeName(m)` (e.g. "Builder") and `ModeDescription(m)` (one-liner)
  for UI display.
- The TUI (`internal/tui/`) exposes mode switching via the **Tab** key (when
  the input is empty) and the `/mode [name]` command; the active mode is shown
  in the status bar and footer. TUI files are split by concern: `tui.go`
  (model/update), `commands.go` (slash commands), `sessions.go`, `approvals.go`,
  `view.go`, plus the rendering helpers (`styles.go`, `markdown.go`, `diff.go`).

---

## 5. Memory / history (`internal/memory/history.go`)

- Append-only log; `Messages()` returns the last `Window` messages.
- **The well-formedness invariant (the most important thing in the codebase):**
  a window trim **never** leaves an orphaned `tool` result or a tool-call group
  (assistant message + its tool results) incomplete — model servers reject that.
  - Trim drops leading orphan `tool` messages.
  - Trim drops any tool-call group that lost some of its results, re-trimming until
    the first message is complete.
  - `Truncate(n)` rolls back partial steps on error.
- `Load()` (resuming a saved session) calls `sanitizeMessages()`: drops system
  messages (the agent prepends its own), orphan `tool` results, incomplete tool
  groups, and empty turns. This is what prevents a crashed session from producing
  a rejected prompt.

**If you change message shaping, re-verify this invariant** (see `history_test.go`
for the test vectors).

---

## 6. Tools (`internal/tools/`)

### Registry & execution
`Registry` holds tools in registration order, indexed by name. `Execute(ctx, name, input)`:
1. Look up the tool; return a helpful "unknown tool" list if missing.
2. Parse JSON args (empty/`{}` → empty map).
3. **Approval gate:** `needApproval(name, args)` — if it needs it, ask via the
   `Approver`; a `nil` approver **denies** (never silently allows).
4. Resolve the paths the tool will mutate (`mutationPaths`), notify mutation
   hooks (checkpoints snapshot each path), then run `Handler(ctx, args)`.
5. On success, count the mutation; cap the result at `maxToolResult` (96KB) via
   `truncateText` (head + tail) and redact credentials (unless disabled).

### Built-in tools (~38)
Registered in `registry.go` in groups:
- **Discovery/reading:** `list_dir`, `glob`, `project_info`, `repo_map`,
  `file_read`, `file_read_many`, `file_stat`, `search`, `hash_file`, `json_query`
- **Semantic search:** `semantic_search` (registered by `agent_build.go` only
  when `embedding_model` is set; cached under `<dataDir>/embeddings`)
- **Editing/filesystem:** `file_edit`, `multi_edit`, `replace_in_files`,
  `file_write`, `make_dir`, `file_move`, `file_copy`, `file_delete`, `diff_files`
- **Git:** `git_status`, `git_diff`, `git_log`, `git_show`, `git_blame`
- **Web/system:** `http_fetch`, `web_search`, `download_file`, `system_info`,
  `env_get`, `which`, `port_check`
- **Verification:** `diagnostics` (detects Go/Rust/Node/Python checks)
- **Delegation:** `subagent` (read-only nested agent, injected from `features.go`)
- **Planning:** `todo_write`, `todo_read`
- **Shell:** `shell` (approval-gated; disabled with `--no-shell`)
- **Time:** `time`

`web_search` scrapes DuckDuckGo's HTML endpoints, so it needs no API key; it is
read-only and not approval-gated. `semantic_search` chunks text files (60-line
windows, 8-line overlap, ignored build/VCS directories, 512 KB per-file cap),
embeds them through `/v1/embeddings`, and ranks chunks with cosine similarity.
The cache is invalidated by a file size/mtime fingerprint; `rebuild=true`
forces a refresh. `subagent` runs a fresh `agent.Agent` against
a separate registry with `SetPolicy(readOnlyPolicy)`, which allows reads,
search, git inspection, web fetches, and `repo_map` but refuses every mutation
and `shell`. The nested registry never contains `subagent` itself, so recursion
is impossible.

### Mutation hooks, redaction, and edit previews
- `Registry.AddMutationHook(fn)` receives `(name, args, paths)` **before** a
  mutating handler runs. `main.go` uses it to snapshot each path in
  `internal/checkpoint`.
- `Registry.Mutations()` counts successful mutating calls in the current run;
  `ResetMutations()` is called at the start of every `Agent.Run`. Auto-verify
  only fires when this counter is non-zero.
- `Registry.SetRedactSecrets(bool)` (default from config) passes every tool
  result through `internal/redact.String`. It masks PEM private keys, bearer
  tokens, key/value assignments, and known token prefixes. `env_get` also uses
  `redact.LooksSecret` for name-based masking.
- `Registry.SetApproveEdits(bool)` / `--approve-edits` makes in-project
  `file_write`, `file_edit`, and `multi_edit` ask for approval with a unified
  diff. `previewMutation` computes the result without touching disk and returns
  `""` when a faithful preview is impossible (binary/oversized files, ambiguous
  `old_text`); the normal write path then proceeds without a preview.

Each tool is an OpenAI function schema (`Name`/`Description`/`Parameters` JSON schema
via `objectSchema(...)`) + a `Handler(ctx, args map[string]any) (string, error)`.
Handlers **return plain strings** — errors are encoded as text so the model can
react (retry or report) rather than panicking.

### Approval & safety (`internal/tools/approval.go`)
This is the project's core safety model. **There is no filesystem sandbox.**
Relative paths resolve against `--dir`; absolute, `~`, `..`, and symlinks are
allowed. Instead of blocking, `needApproval()` prompts before:

| Action | Always prompt? |
|---|---|
| `shell` | Yes, every command |
| `diagnostics` | Yes (it runs build/lint programs) |
| `file_delete` | Yes (incl. recursive) |
| `download_file` | Yes |
| `http_fetch` | Non-GET/HEAD only |
| `replace_in_files` | Non-dry-run, or any path outside project |
| `env_get` | `show_secrets=true` |
| In-project `file_write`/`file_edit`/`multi_edit` | Only with `--approve-edits`; the prompt includes a unified diff |
| Out-of-project read/write (`file_write`, `file_edit`, `multi_edit`, `make_dir`,
  `file_move`, `file_copy`, reads, `git_*`) | Yes, if `!insideProject()` |

Approval decisions (`ApprovalDecision`): `Deny`, `Once`, `Always` (this tool for
the session), `All` (every dangerous call for the session). The `approver` callback
is installed by the frontend (TUI channels via Bubble Tea; REPL reads stdin).
A `nil` approver **denies** (safe default).

**Path helpers (`internal/tools/tool.go`):**
- `resolvePath(root, p)` — non-sandbox: relative → against `root`; allows abs,
  `~`, `..`, symlinks. **Current** path resolution for all file tools.
- `safePath(root, p)` — legacy sandbox: rejects abs, `..`, symlink escapes.
  **Retained** for backward compatibility / tests; not used by tools.
- `resolveSymlinks(p)` / `resolveExisting(p)` — resolve symlinks in the deepest
  **existing** ancestor and re-join the nonexistent remainder. This stops a
  symlink inside the project from escaping it, even for nonexistent targets.
- `insideProject(p)` (approval.go) uses `resolveExisting` + `filepath.Rel` to decide
  whether a resolved path stays inside the project.

**SSRF guards (`web.go`):** `http_fetch`/`download_file` resolve DNS and reject
link-local (169.254.x.x, fe80::/10), unspecified (0.0.0.0, ::), and link-local
multicast. Loopback and private (10/8, 192.168/16, 172.16/12) are **intentionally
allowed** so local dev servers can be inspected. Redirects checked every hop
(max 5).

**Shell tool (`shell.go`) invariants:**
- Runs `bash -c` (falls back to `sh`).
- **Process-group kill**: `configureProcessGroup` + `killProcessGroup` +
  `WaitDelay` so `Ctrl+C` kills all children.
- Timeout 1..900s (default 120); output capped to 24KB head+tail.
- ANSI stripped, control chars removed, env forced to `PAGER=cat`, `TERM=dumb`,
  `NO_COLOR=1`, `GIT_EDITOR=true`.
- Honors `ctx` cancellation.

---

## 7. LLM client (`internal/llm/`)

- **`Client.Complete(ctx, messages, tools, onToken)`** → `*Completion`
  (aggregated `Content` + `ToolCalls` + `FinishReason`). Streams via SSE;
  forwards text deltas to `onToken`.
- **SSE robustness:** parses `data:` events; fast path is one JSON object per line
  (the norm); buffers **multi-line** events per the SSE spec until JSON is valid;
  handles servers that ignore `stream:true` and return a single JSON object
  (`readJSONCompletion`). Max stream buffer 8MB.
- **Streamed tool-call deltas:** aggregates `Function.Arguments` across multiple
  deltas, handling both indexed and unindexed `tool_calls` (continuation → most
  recent call). If a server omits the `id`, it invents a stable `call_N`.
- **Provider presets** (`provider.go`): `llama-prism` (9931), `lmstudio` (1234),
  `ollama` (11434), `openai`, `openrouter`, `groq`, `together`, `compatible`.
  `Resolve()` with `auto` probes the three local servers in parallel (short
  timeouts); picks a model when exactly one is loaded/available; otherwise
  requires `--model`. `--list-models` enumerates reachable providers.
- **Errors:** `ClientError` wraps `Cause`/`Status`/`Body`. `IsModelNotLoaded`
  detects "not loaded"/"not found + model" in the body and yields a clear
  `MODEL NOT LOADED` message (no raw HTTP dump).
- **`Router`** (`router.go`) is the `Completer` used by `main.go`. It owns the
  active client/model and an ordered fallback chain (`--fallback-models`).
  `Complete` tries the active model, then each fallback; a successful fallback
  becomes active and calls the switch callback. `SetModel`/`Model` power
  `/model`; the router is safe for concurrent readers (client/model behind an
  `RWMutex`). Streaming output from a failed attempt may already have been
  emitted, so fallbacks are best for "model unavailable" failures rather than
  mid-stream errors.
- **URL handling** (`endpointURLs`): accepts a host URL, `host/v1`, or even a full
  chat URL; normalizes to `/v1/chat/completions` + `/v1/models`.

---

## 8. MCP (`internal/mcp/`)

`mcp.Connect(ctx, ServerConfig)` supports **stdio** (spawn a process) and
**HTTP** (`streamable` default, or `sse`) transports. Tools are listed via
`ListTools`, called via `Call`, and **registered as external tools** in the
registry, namespaced `<server>__<tool>`.

**Critical invariant:** MCP tools run on a server Metis does **not** control,
so **every MCP tool call goes through the approval gate** (unless
`auto_approve`). `main.go` builds an `mcp.Manager` (`internal/mcp/manager.go`) that
registers them via `RegisterExternalTool(t, serverName)` and, on reconnect/remove,
via `Registry.ReloadMCP(name, tools)`. They are marked in `Registry.external`.
A failing server is logged as a warning and **skipped** (agent runs with the
rest of the tools) — never fatal.

### Runtime management (`/mcp` command + `ServerManager`)

MCP servers can be added, removed, and reconnected **at runtime** via the
`/mcp` slash command (both TUI and plain REPL), without restarting. `main.go`
constructs an `mcp.Manager` (see `internal/mcp/manager.go`) around the registry
and persists the configured servers to the project config file
(`./.metis-agent.json`). On startup, `Manager.Load(ctx)` reads the config and
reconnects each server; on exit, `Manager.CloseAll()` tears them down.

`/mcp` subcommands (parse the rest of the line as subcommand + args):
- `/mcp` or `/mcp list` — list servers + their namespaced tools (`<server>__<tool>`).
- `/mcp add {json}` — add a server, e.g.
  `/mcp add {"type":"stdio","name":"github","command":"npx","args":["-y","@modelcontextprotocol/cli"]}`
  or `/mcp add {"type":"http","transport":"streamable","url":"https://mcp.example.com/mcp"}`.
  On connect failure the server is still persisted (listed as *not connected*)
  so it can be retried.
- `/mcp remove <name>` — disconnect and remove.
- `/mcp reconnect <name>` — drop and re-establish the connection.

Adding/removing updates `Registry.ReloadMCP(name, tools)` so the agent's tool
list changes immediately (new MCP tools become callable in the next turn; removed
ones disappear). The `Manager` keeps its own map of servers so it can re-add
tools on reconnect without dropping the built-in toolset.

Config: `mcp_servers` array in config file or `METIS_MCP_SERVERS` env.
`name` optional (derived from command path for stdio or URL host for http).
Stdio: `command` + `args` + optional `env` (overrides `os.Environ`).
HTTP: `url` + `transport` + `auth_type: bearer`/`auth_token` + `headers`.

`cmd/mcp-smoke-server` is a tiny stdio MCP server (greet + fail) used **only**
by integration tests to exercise connect/list/call.

---
## 9. Skills (`internal/skills/`)

Metis supports **skills**: reusable, named pieces of guidance (a `SKILL.md`
file) that the agent can discover and use. A skill has:

- `name` — a short identifier (or the folder name).
- `description` — when to use it (this is what the agent reads first).
- an optional `run` — a command the skill declares it can run.
- a markdown **body** — the reference material the agent consults.

### Search paths (priority order)

1. `<project>/.metis-agent/skills/` — project-local skills (per project).
2. `$METIS_SKILL_PATHS` — extra dirs (colon-separated).
3. `~/.agents/skills/` — user-level globals (e.g. `find-skills`, `frontend-design`).

(Override the global dir with `$METIS_GLOBAL_SKILLS_DIR`.)

A skill is the folder's `SKILL.md`. `skills.Manager` loads them on startup and
caches them; `Invalidate()` re-reads them after a skill file is added/removed.

### The `skills` tool (model-facing)

The model can call the `skills` tool with:

- `{"action":"list"}` — list all skills (name, description, run command).
- `{"action":"info","name":"x"}` — show a skill's full body.
- `{"action":"use","name":"x"}` — load a skill's body into context so the agent
  can consult it for the rest of the task.
- `{"action":"run","name":"x"}` — execute the skill's declared `run` command.

**Critical invariant:** `run` is **approval-gated exactly like `shell`**. It is
routed through the registry's `Execute("shell", ...)` so `needApproval()` and
the user's approver apply. Skills can run commands, but the user always confirms
each run. The `defaultSystemPrompt` in `main.go` teaches the model to use
`list`/`info` before acting, and to prefer `skills run` over shelling out
directly when a skill's `run` is available.

### `/skills` command (user-facing)

Both the TUI and the plain REPL support `/skills [subcommand]`:
- `/skills` or `/skills list` — list.
- `/skills info <name>` / `use <name>` — show / load.
- `/skills run <name>` — execute the skill's command (approval-gated, via
  `a.Registry().Execute("shell", ...)`).



## 10. Sessions (`internal/session/`)

- One JSON file per session, under a **per-project hashed dir**
  (`sha256(abs workdir)[:12]`) in the platform data dir
  (`~/.local/share/metis-agent/sessions` on Linux, etc. — see `config.DataDir()`).
- **Atomic save:** write to a temp file → `Sync()` → `Rename()` (Windows has a
  fallback: remove target, then rename).
- **Session IDs:** `YYYYMMDD-HHMMSS` + 3 hex random, or a **user-named** session
  validated against `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`.
- **Loading:** `Store.Load` → agent's `LoadHistory` → `sanitizeMessages` (see §5)
  so a crashed session can't produce a rejected prompt.
- **Listing:** `/sessions` shows recent sessions (newest first) with a preview
  of the first user message.

---

## 11. TUI (`internal/tui/`)

Bubble Tea model (`tui.go`, ~1400 lines):
- **Blocks:** `blockKind` = user/assistant/tool/error/system/welcome. Each block
  caches its rendered form (Markdown + chroma highlighting + diff rendering is
  expensive; the transcript rebuilds on every token).
- **Streaming:** agent events route through `p.Send(AgentEventMsg(ev))` — safe
  to call from the agent goroutine.
- **Approval:** an approval request is sent as a message; the agent goroutine
  **blocks** on a channel `resp` until the user answers (or `ctx` cancelled →
  deny). Approve keys: `y`/`a`/`A`/`n`/Esc.
- **Input:** textarea; Enter submits; **Alt+Enter / Ctrl+J** inserts a newline.
  Ctrl+P/N scroll message history. PgUp/PgDn + mouse wheel scroll the viewport.
- **Transcript:** per-step tool output, **expandable with Ctrl+O** (`expandTools`).
- **Status:** live context meter (`ctx <messages>/<window>`), current model from
  the router (`ModelName`), and active mode.
- **Slash menu:** type `/`, filter, arrow-key to select, Tab/Enter to complete,
  Enter to run. Esc dismisses.
- **Keys:** Enter send · Alt+Enter/Ctrl+J newline · Esc/Ctrl+C cancel run ·
  Ctrl+L clear transcript · Ctrl+R clear + reset conversation ·
  Ctrl+C idle / Ctrl+D quit · y/a/A/n approval · ↑/↓/Tab/Enter slash menu.
- **Markdown/rendering:** `markdown.go` (goldmark + glamour), `highlight.go`
  (chroma), `diff.go` (unified diff).
- **Long operations:** approval previews can be multi-line; `/model` blocks up
  to 10s while listing; `/compact` and `/commit` run through the spinner and
  respect Esc cancellation.

Slash commands (TUI): `/help /clear /reset /status /tools /expand /session /sessions /new /resume [id] /autoapprove [on|off] /approve-edits [on|off] /model [name] /compact /context /undo /commit [message] /mcp /skills /mode /quit`.

`/commit [message]` stages everything with `git add -A`, then commits. When the
message is empty it asks the model for a **Conventional Commits** message
(imperative mood, scope when obvious) based on `git status --porcelain` +
`git diff HEAD`; if the model is unavailable or returns nothing it falls back
to `chore: update project files`. `git status --porcelain` is checked first —
a clean tree returns "nothing to commit" without calling the model.
Plain REPL supports the same set except `/expand`, `/session`, `/sessions`,
`/new`, `/resume`, and `/mode`; entering `/` prints the full list.
`/mcp [list|add|remove|reconnect]` manages MCP servers (see §8).

### ACP server (`internal/acp/`)

`metis-agent --acp` speaks JSON-RPC 2.0 over stdin/stdout using newline-delimited
messages. The reader loop dispatches each request in its own goroutine so
`session/cancel` and permission responses stay live while a prompt runs.

- `initialize` returns protocol version 1, `agentInfo`, and the supported
  capabilities (`agentCapabilities.loadSession: false`, `promptCapabilities.embeddedContext:
  true`, `image`/`audio` false). It also handles `authenticate` (returns an
  empty object — Metis does not require auth).
- `session/new` validates an absolute `cwd`, calls the factory from
  `agent_build.go`, and installs a per-session approver.
- `session/prompt` flattens text/resource content and calls `Agent.Run`.
  Streaming text becomes `agent_message_chunk`; each tool call becomes
  `tool_call` + `tool_call_update` (pending → completed/failed). The response
  is `{stopReason: end_turn|max_turn_requests|cancelled}`.
- Approvals call `session/request_permission` with `allow_once`,
  `allow_always`, and `reject_once` options; the diff preview is included as
  content blocks. `session/cancel` (sent as a notification, no id) cancels the
  active run's context.
- `session/set_mode` takes a `modeId` (`general`/`0`, `planner`/`1`, `builder`/`2`,
  `debugger`/`3`, `cybersecurity`/`4`, `simplifier`/`5`, or a number) and calls
  `Agent.SetMode`, emitting a `current_mode_update` session update. Unknown
  mode ids are rejected with a `-32602` error.
- `session/close` deletes the session and cancels any running prompt, then runs
  the factory's cleanup. A `sessionId` that isn't registered (e.g. calling close
  on an unknown id, or using a stale id after close) returns a `-32002` error
  rather than crashing.
- Text and resource blocks are supported; image/audio blocks are not advertised.
  `session/load`, client `fs/*`/`terminal/*`, and per-session `mcpServers` are
  intentionally not implemented yet.
- The ACP server must be the only writer on stdout. Diagnostics and warnings go
  to stderr; do not add `fmt.Print*` calls to the ACP path.

---

## 12. How to make a change — conventions & workflow

### Coding conventions
- **Idiomatic Go, no linter-config beyond `go vet`.** `gofmt` your files.
- **Errors as plain strings for tool handlers** (so the model can react).
  Internal package errors use `fmt.Errorf("...: %w", err)` wrapping.
- **Thread-safety:** the TUI and agent run in separate goroutines. Agent state
  is protected by `a.mu`; the TUI routes events through `p.Send`. Don't call
  blocking work from `OnEvent`.
- **Path handling:** use `resolvePath` for tools (non-sandbox). Only reach for
  `safePath` if you're deliberately re-implementing sandbox semantics.
- **Approval:** if you add a new destructive/out-of-project tool, you **must**
  add it to `needApproval()` in `approval.go`, or it will run unprompted.
  New MCP tools are auto-covered (external).
- **Mutation tracking:** if the tool changes local files, add its argument →
  path mapping to `Registry.mutationPaths` so checkpoints and auto-verify see
  it. Without an entry the call is treated as read-only.
- **History:** never shape messages in a way that breaks the well-formedness
  invariant (§5). Add test vectors if you change `Messages()`/`sanitizeMessages`.
- **Config:** add new fields to `Values` + `File` (pointer) + `ApplyEnv` (a
  `METIS_*` var) + flag in `main.go` + `Validate()` + `WriteDefault()`. Keep
  precedence order.

### Adding a new built-in tool (checklist)
1. Implement `Handler(ctx, args map[string]any) (string, error)` in the appropriate
   `internal/tools/*.go` (or a new file).
2. Build the `Tool{ Name, Description, Parameters: objectSchema(...) }`.
3. `r.Add(NewXTool(...))` in `registry.go` in the right group.
4. If destructive / out-of-project / network, add a case to `needApproval()`
   in `approval.go`; if it writes files, add a case to `mutationPaths`.
5. If a read-only tool should be available to `subagent`, add its name to
   `readOnlyPolicy` in `features.go`.
6. Add a test in the matching `*_test.go` (or `tool_test.go`).
7. Document the tool in `README.md` (Tools table) and the system prompt in
   `main.go` (`baseSystemPrompt` / `helpText`) if it changes behavior.

### Adding a new MCP transport or server config field
Update `ServerConfig`, `Connect()`, `DefaultServerName()`, and the config
docs. Remember: all MCP tools **stay external** (approval-gated).

### Testing conventions
- Unit tests live in `*_test.go` beside the package. New packages
  (`checkpoint`, `redact`, `instructions`, `update`) ship with focused tests.
- Use fake models for the agent loop (`internal/agent/agent_test.go` shows the
  pattern with a `Completer` stub).
- `cmd/mcp-smoke-server` is the fixture for MCP integration tests.
- The TUI is tested at a light level (`tui_test.go`) — rendering is expensive.
- The root package tests (`features_test.go`) cover flag helpers, the read-only
  policy, verification, and the NDJSON sink.
- **`go test ./...` must pass; `go test -race ./...` must pass.**
  **Don't delete existing test vectors.**

### Verification (run these after any change)
```sh
cd ~/metis-agent
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
go build -o metis-agent .
# smoke the binary
./metis-agent --help
./metis-agent --list-models          # (needs a reachable local server)
./metis-agent --once "What time is it?"   # (needs a model server)
```
If you change the system prompt, re-check `README.md`'s tool list stays
consistent (it mirrors `defaultSystemPrompt`).

---

## 13. Safety model — the non-negotiables

This is the project's core design. **Do not "improve" it by adding a sandbox
that blocks legitimate workflows; do not remove the approval gate either.**

1. **No filesystem sandbox.** Absolute paths, `~`, `..`, symlinks are allowed.
   The **only** protection is the approval prompt (or `--auto-approve`).
2. **Approval is the safety net.** Every destructive/out-of-project action is
   gated in `needApproval()`. A `nil` approver **denies** (never allows).
3. **MCP tools are untrusted.** Always external, always approval-gated.
4. **Shell is powerful but controlled.** Process-group kill + timeout + output
   cap + ANSI cleanup + approval.
5. **Context-flooding prevention.** Tool results capped at 96KB; shell output
   capped at 24KB (head+tail); stream buffer 8MB.
6. **Session atomicity.** Temp file + Sync + Rename; never a torn file.
7. **SSRF guards** block link-local/cloud-metadata but allow loopback/private
   (intentional, for local dev servers).
8. **Undo is a safety net, not a sandbox.** Mutation hooks snapshot paths before
   writes; `/undo` restores the previous turn. Skipped (too large/budget) paths
   are reported, never silently claimed as restored.
9. **Redaction is on by default.** Tool output passes through
   `internal/redact` unless the user explicitly disables it. Do not redact the
   model's own final answer or user input.
10. **Updates verify checksums.** `--update` accepts only the matching platform
    asset and refuses a missing/mismatched `SHA256SUMS` entry.

---

## 14. Quick reference: where to find things

| Question | Where |
|---|---|
| How does the agent loop run? | `internal/agent/agent.go` |
| How is the window kept well-formed? | `internal/memory/history.go` |
| How is a tool executed + approved? | `internal/tools/registry.go` + `approval.go` |
| How is a new tool added? | `internal/tools/*.go` + `registry.go` + `approval.go` |
| How does streaming SSE work? | `internal/llm/client.go` |
| How is a provider resolved? | `internal/llm/provider.go` |
| How does the TUI render/approve? | `internal/tui/tui.go` |
| How is config loaded/merged? | `internal/config/config.go` + `main.go` |
| How are sessions saved? | `internal/session/session.go` |
| How does `/undo` snapshot files? | `internal/checkpoint/checkpoint.go` |
| How are credentials redacted? | `internal/redact/redact.go` |
| How are AGENTS.md/rules loaded? | `internal/instructions/instructions.go` |
| How does `--update` work? | `internal/update/update.go` |
| How does `--doctor` work? | `internal/doctor/doctor.go` |
| How do model fallbacks work? | `internal/llm/router.go` + `features.go` |
| How does editor (ACP) mode work? | `internal/acp/acp.go` + `agent_build.go` |
| How does semantic search work? | `internal/embeddings/embeddings.go` + `internal/tools/semantic.go` |
| How are MCP tools wired? | `internal/mcp/mcp.go` + `internal/mcp/manager.go` + `main.go` |
| How are skills discovered/used? | `internal/skills/skill.go` + `manager.go` + `main.go` |
| What tools exist + their schemas? | `internal/tools/*.go` (`registry.go` order) |
| How to build/test? | §12 (How to make a change, verification) |
| How do the installers work? | §15 + `scripts/install.sh` + `scripts/install.ps1` |

---

## 15. Installer & releases (`scripts/install.sh`, `scripts/install.ps1`, `release.yml`)

The installers are the supported path for end users. POSIX has two modes:

- **easy** (default): installs Ollama, pulls `gemma4:e2b`, writes the global
  config, installs the launcher.
- **advanced**: picks Ollama / llama.cpp / LM Studio, reuses an installed model,
  and when none exists recommends a model from detected RAM (`gemma4:e2b`
  below 16 GB, `gemma4:e4b` above).

`--uninstall` removes the binary, launcher, and the PATH block from bash/zsh/
fish rc files. It keeps config/data/models by default; `--purge` removes them.
`install.sh` never uninstalls Ollama, llama.cpp, or LM Studio.

`scripts/install.ps1` is the native Windows path (Windows 10 1803+ for bundled
`tar.exe`). It resolves the latest tag through the GitHub API, downloads
`metis-agent_<version>_windows_amd64.tar.gz`, verifies `SHA256SUMS`, extracts
with `tar.exe`, installs to `%LOCALAPPDATA%\metis-agent\bin`, updates the user
`PATH`, and can install Ollama with `winget` and pull `gemma4:e2b`. Config goes
to `%APPDATA%\metis-agent\config.json` and is written without a UTF-8 BOM.
`-Uninstall`/`-Purge` mirror the POSIX flow. `windows/arm64` is not published
yet and the script says so instead of installing the wrong binary.

Layout POSIX creates:

| Path | Purpose |
|---|---|
| `$XDG_DATA_HOME/metis-agent/bin/metis-agent` (default `~/.local/share/...`) | real binary (macOS: `~/Library/Application Support/metis-agent`) |
| `$PREFIX/bin/metis-agent` (default `~/.local/bin/...`) | generated launcher; starts the backend on demand |
| `$PREFIX/bin/Metis-Agent` | capitalized alias (case-sensitive filesystems) |
| `$XDG_CONFIG_HOME/metis-agent/config.json` (macOS: `~/Library/Application Support/metis-agent/config.json`) | global config |
| `$XDG_DATA_HOME/metis-agent/backend.log` | backend stdout/stderr |

The paths must match `internal/config`: Linux honours `XDG_CONFIG_HOME` and
`XDG_DATA_HOME`; macOS uses `~/Library/Application Support/metis-agent` for
both because `os.UserConfigDir` ignores XDG there; Windows uses
`%APPDATA%` for config and `%LOCALAPPDATA%` for data.

**Release asset naming is a contract.** Both installers download:

```
/releases/latest/download/metis-agent_<os>_<arch>.tar.gz
/releases/latest/download/SHA256SUMS
/releases/download/v<version>/metis-agent_<version>_<os>_<arch>.tar.gz
```

Each tarball contains a single `metis-agent` (or `metis-agent.exe` on Windows)
at its root. `release.yml` publishes both the versioned and stable names and a
`SHA256SUMS` covering both, and injects the version with
`-ldflags "-X main.version=${version}"`. `main.go` reads it in `--version`,
`--check-update`, and `--update`; development builds report `dev`. If you rename
assets, update both installers and `internal/update` in the same commit. When no
release exists the POSIX installer falls back to `git clone` + `go build`.

The generated launcher embeds the backend choice at install time, so changing
`config.json` by hand does not change which process the launcher starts. The
launcher runs the binary in the **current** directory (not `$HOME`); that is
intentional for a coding agent. `--help`/`--version` bypass backend startup.

Prompting reads from `/dev/tty` (fd 3), so `curl | bash` stays interactive;
without a TTY (CI, containers) the installer takes the documented defaults and
`--yes` semantics.

