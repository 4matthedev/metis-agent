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

**metis-agent** is a local-first coding agent written in **Go 1.24+**. It talks
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
├── main.go                     # CLI flags (24), TUI/REPL/one-shot entry points,
│                               #   provider resolution, session open/save, MCP wiring
├── go.mod                      # module metis-agent; Go 1.25; bubbletea/bubbles/
│                               #   lipgloss + MCP go-sdk
├── go.sum
├── cmd/
│   └── mcp-smoke-server/main.go  # Tiny MCP stdio server (greet/fail tools)
│                                 #   — used ONLY by integration tests
├── internal/
│   ├── agent/                  # The agent loop (see §4)
│   │   ├── agent.go            # Agent, Run(), event emission, history ops
│   │   └── agent_test.go       # fake model tests
│   ├── config/                 # JSON config + env + platform paths
│   │   ├── config.go           # Values, File, Defaults(), ApplyEnv(), Validate()
│   │   └── config_test.go
│   ├── llm/                    # OpenAI-compatible client
│   │   ├── client.go           # SSE parser, JSON fallback, streaming, model-not-loaded
│   │   ├── provider.go         # provider presets, Resolve(), probing, --list-models
│   │   ├── client_test.go
│   │   └── provider_test.go
│   ├── memory/                 # Conversation history
│   │   ├── history.go          # sliding window + well-formedness invariant
│   │   └── history_test.go
│   ├── mcp/                    # MCP client + runtime server management
│   │   ├── mcp.go              # ServerConfig, Connect(), ListTools, Call, namespacing
│   │   ├── manager.go          # Manager: /mcp add/remove/reconnect/list,
│   │   │                       #   config persistence + registry reload
│   │   └── manager_test.go
│   ├── skills/                 # Skill system: discover, load, run SKILL.md files
│   │   ├── skill.go            # Skill type + ParseSkill (frontmatter + body)
│   │   ├── manager.go          # Manager: List/Get/Command/RunCommand, search paths
│   │   └── manager_test.go
│   ├── session/                # Per-project session persistence
│   │   └── session.go          # Session, Store, atomic Save()
│   ├── tools/                  # Tool registry + 33 built-in tools + approval
│   │   ├── registry.go         # Registry, Execute(), approval gating
│   │   ├── approval.go         # needApproval(), ApprovalDecision/Approver, insideProject()
│   │   ├── tool.go             # Tool type, schema helpers, path resolvers
│   │   ├── file.go             # list_dir, glob, project_info, file_read(_many),
│   │   │                        #   file_stat, search, hash_file, json_query
│   │   ├── fs.go               # file_edit, multi_edit, replace_in_files, file_write,
│   │   │                        #   make_dir, file_move/copy/delete, diff_files
│   │   ├── git.go              # git_status/diff/log/show/blame
│   │   ├── project.go          # language/manifest/git detection, project_info
│   │   ├── json.go             # json_query
│   │   ├── system.go           # system_info, env_get, which, port_check
│   │   ├── todo.go             # in-session todo store
│   │   ├── web.go              # http_fetch, download_file (+ SSRF guards)
│   │   ├── shell.go            # shell tool: exec, process group kill, output cap, ANSI
│   │   ├── proc_unix.go / proc_other.go   # process-group kill (platform-specific)
│   │   └── tool_test.go
│   └── tui/                    # Bubble Tea UI
│       ├── tui.go              # Model, Run(), input, approval, slash menu, viewport
│       ├── styles.go           # palette, transcript rendering
│       ├── highlight.go        # chroma-based syntax highlighting
│       ├── markdown.go         # glamour-based Markdown rendering
│       ├── diff.go             # unified diff rendering
│       └── tui_test.go
```

### Dependency graph (who calls whom)
```
main.go ──> internal/llm      (resolve provider + model, build Client)
           ──> internal/config (load + merge config)
           ──> internal/tools  (build Registry)
           ──> internal/mcp    (connect MCP servers, register external tools)
           ──> internal/session (open/save session)
           ──> internal/agent  (Agent; owns the loop)
                 ──> internal/memory (History)
           ──> internal/tui    (TUI/REPL entry)

internal/agent   ──> llm (Complete), tools (Registry.Execute), memory (History)
internal/tools   ──> llm (schema types only), config (not) — self-contained executors
internal/tui     ──> agent (events), session, tools (approval types)
internal/session ──> llm (Message), config (DataDir)
internal/mcp     ──> mcp go-sdk
```
**Rule of thumb:** `internal/llm` is a leaf (no internal deps). `internal/agent`
depends on `llm`, `memory`, `tools`. `internal/tui` depends on `agent`,
`session`, `tools`. Never add a dependency *up* the graph (e.g. don't import
`internal/tools` from `internal/llm`).

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
| Env | `METIS_PROVIDER`, `METIS_MODEL`, `METIS_BASE_URL`, `METIS_API_KEY`, `METIS_TEMPERATURE`, `METIS_MAX_STEPS`, `METIS_WINDOW`, `METIS_SHELL_TIMEOUT_SEC`, `METIS_AUTO_APPROVE`, `METIS_MCP_SERVERS` |
| Flags | `--provider`, `--model`, `--base-url`, `--temp`, `--max-steps`, `--window`, `--dir`, `--auto-approve`, `--once`, … |

The config file shape (`internal/config/File`) uses **pointers** so an unset field
doesn't override a zero value. `File.Apply(&v)` overlays only non-nil fields.
`METIS_MCP_SERVERS` is a JSON array of `mcp.ServerConfig`, merged over file
servers (same-name entries from env win).

Key flags (defaults): `--temp 0.3`, `--max-steps 40`, `--window 64`,
`--shell-timeout 120` (clamped 1–900 in the shell tool), `--dir .`,
`--no-shell`, `--plain`, `--once`, `--session NAME`, `--resume`, `--no-session`,
`--list-models`, `--init-config`, `--highlight auto|dark|light|off`.

---

## 4. The agent loop (`internal/agent/agent.go`)

`Agent.Run(ctx, user)` does, per user task:

1. `history.Append(user message)`.
2. Repeat up to `MaxSteps`:
   - send `system prompt + windowed history + tool schemas` with `stream: true`.
   - stream text deltas to the UI via `EventText`.
   - **If tool calls:** append an `assistant` message with `ToolCalls`; run each
     tool **in order** (so cancellation stops a long command); append a `tool`
     message per result; **continue** (another model turn).
   - **If final answer** (no tool calls, non-empty): append `assistant` message,
     emit `EventDone`, return.
3. On `ctx` cancel, `Complete` error, or empty-turn exhaustion: `history.Truncate(base)`
   to roll back the partial step, emit `EventError`, return.

**Key invariants to preserve:**
- **One model turn per step**, and the history is only advanced atomically on
  success — errors roll back to `base`.
- **Event delivery is from the running goroutine.** `OnEvent` callbacks must not
  block (the TUI routes events through Bubble Tea's `p.Send`, which is safe).
- The agent holds `a.mu` for the **entire duration of `Run`** — never re-enters
  or holds other locks. `SetEventSink`/`SetApprover` etc. take the lock too.
- `Completer` is an interface (`Complete(ctx, messages, tools, onToken)`), so the
  loop is testable with a fake model (`internal/agent/agent_test.go`).

**Events (`agent.Event`):** `EventStep`, `EventText`, `EventToolCall`,
`EventToolResult`, `EventDone`, `EventError`. Each carries `Step`, `Text`,
`Tool`, `Args`, `Result`, `Err`, `Duration`.

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
4. Run `Handler(ctx, args)`; return a string (errors as `"error: …"`).
5. **Cap the result at `maxToolResult` (96KB)** via `truncateText` (head + tail)
   so long output can't flood the model context.

### Built-in tools (~33)
Registered in `registry.go` in groups:
- **Discovery/reading:** `list_dir`, `glob`, `project_info`, `file_read`,
  `file_read_many`, `file_stat`, `search`, `hash_file`, `json_query`
- **Editing/filesystem:** `file_edit`, `multi_edit`, `replace_in_files`,
  `file_write`, `make_dir`, `file_move`, `file_copy`, `file_delete`, `diff_files`
- **Git:** `git_status`, `git_diff`, `git_log`, `git_show`, `git_blame`
- **Web/system:** `http_fetch`, `download_file`, `system_info`, `env_get`,
  `which`, `port_check`
- **Planning:** `todo_write`, `todo_read`
- **Shell:** `shell` (approval-gated; disabled with `--no-shell`)
- **Time:** `time`

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
| `file_delete` | Yes (incl. recursive) |
| `download_file` | Yes |
| `http_fetch` | Non-GET/HEAD only |
| `replace_in_files` | Non-dry-run, or any path outside project |
| `env_get` | `show_secrets=true` |
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
- **Slash menu:** type `/`, filter, arrow-key to select, Tab/Enter to complete,
  Enter to run. Esc dismisses.
- **Keys:** Enter send · Alt+Enter/Ctrl+J newline · Esc/Ctrl+C cancel run ·
  Ctrl+L clear transcript · Ctrl+R clear + reset conversation ·
  Ctrl+C idle / Ctrl+D quit · y/a/A/n approval · ↑/↓/Tab/Enter slash menu.
- **Markdown/rendering:** `markdown.go` (goldmark + glamour), `highlight.go`
  (chroma), `diff.go` (unified diff).

Slash commands (TUI): `/help /clear /reset /status /tools /expand /session /sessions /new /resume [id] /autoapprove [on|off] /mcp /skills /quit`.
Plain REPL supports: `/help /reset /status /tools /autoapprove [on|off] /mcp /skills /quit`.
`/mcp [list|add|remove|reconnect]` manages MCP servers (see §8).

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
- **History:** never shape messages in a way that breaks the well-formedness
  invariant (§5). Add test vectors if you change `Messages()`/`sanitizeMessages`.
- **Config:** add new fields to `Values` + `File` (pointer) + `ApplyEnv` (a
  `METIS_*` var) + flag in `main.go` + `Validate()`. Keep precedence order.

### Adding a new built-in tool (checklist)
1. Implement `Handler(ctx, args map[string]any) (string, error)` in the appropriate
   `internal/tools/*.go` (or a new file).
2. Build the `Tool{ Name, Description, Parameters: objectSchema(...) }`.
3. `r.Add(NewXTool(...))` in `registry.go` in the right group.
4. If destructive / out-of-project / network, add a case to `needApproval()`
   in `approval.go`.
5. Add a test in the matching `*_test.go` (or `tool_test.go`).
6. Document the tool in `README.md` (Tools table) and the system prompt in
   `main.go` (`defaultSystemPrompt` / `helpText`) if it changes behavior.

### Adding a new MCP transport or server config field
Update `ServerConfig`, `Connect()`, `DefaultServerName()`, and the config
docs. Remember: all MCP tools **stay external** (approval-gated).

### Testing conventions
- Unit tests live in `*_test.go` beside the package (no test files for
  `session`, `config` — if you change them, consider adding coverage).
- Use fake models for the agent loop (`internal/agent/agent_test.go` shows the
  pattern with a `Completer` stub).
- `cmd/mcp-smoke-server` is the fixture for MCP integration tests.
- The TUI is tested at a light level (`tui_test.go`) — rendering is expensive.
- **`go test ./...` must pass; `go test -race ./...` must pass.**
  Target coverage for core packages: `agent` ~73%, `llm` ~37%, `memory` ~53%.
  Overall ~27%. **Don't delete existing test vectors.**

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
| How are MCP tools wired? | `internal/mcp/mcp.go` + `internal/mcp/manager.go` + `main.go` |
| How are skills discovered/used? | `internal/skills/skill.go` + `manager.go` + `main.go` |
| What tools exist + their schemas? | `internal/tools/*.go` (`registry.go` order) |
| How to build/test? | §12 (How to make a change, verification) |
