# metis-agent

metis-agent is a Go coding agent for local and hosted OpenAI-compatible model
servers (llama-prism, LM Studio, Ollama, OpenAI, OpenRouter, Groq, Together,
and any compatible endpoint). It explores a project, edits files precisely,
runs builds and tests, inspects git history, and iterates until the task works.
It ships with a full-screen TUI, a line-based REPL, and a one-shot mode.

```
▌ metis  coding agent                              Qwen3.8-27B · 127.0.0.1:9931
──────────────────────────────────────────────────────────────────────────────
  workspace
  ────────────────────────────────────────────────────────────
  model    Qwen3.8-27B
  server   127.0.0.1:9931
  project  /home/you/project
  approval ask
  tools    shell · file_edit · project_info · git_diff · todo_write

  Type a task and press Enter.  /help for commands.

you    fix the failing test in calc.py
metis  Let me inspect the project first.
  list_dir   .  (depth 2)
  ok         calc.py (71 B)  ·  1ms
  shell      $ python -m unittest -v
  failed     exit code: 1  ·  48ms
             test_add ... FAIL
             AssertionError: -1 != 5
  file_edit  calc.py
  ok         edited calc.py (1 replacement(s))  ·  1ms
  shell      $ python -m unittest -v
  ok         exit code: 0  ·  50ms
             Ran 2 tests in 0.000s
metis  Fixed `add` (it was subtracting) and both tests pass.

 ● ready                                                  5 steps · 1.2s
──────────────────────────────────────────────────────────────────────────────
 › Ask Metis to do something…
──────────────────────────────────────────────────────────────────────────────
 enter send  ·  alt+enter newline  ·  ctrl+o tools  ·  /help  ·  ctrl+c quit
```

## Install

One line gets a working local setup. **Easy mode** installs Ollama, pulls
`gemma4:e2b`, installs Metis, and configures everything:

```sh
curl -fsSL https://raw.githubusercontent.com/4matthedev/metis-agent/main/scripts/install.sh | bash
```

**Advanced mode** lets you choose Ollama, llama.cpp, or LM Studio. It reuses a
model you already have and, when none is found, picks a Gemma variant based on
detected RAM (`gemma4:e2b` below 16 GB, `gemma4:e4b` above):

```sh
curl -fsSL https://raw.githubusercontent.com/4matthedev/metis-agent/main/scripts/install.sh | bash -s -- --advanced
```

On Windows, use PowerShell (Windows 10 1803+ for bundled `tar.exe`):

```powershell
irm https://raw.githubusercontent.com/4matthedev/metis-agent/main/scripts/install.ps1 | iex
```

It installs Ollama through `winget` when needed, pulls `gemma4:e2b`, verifies
the release checksum, installs under `%LOCALAPPDATA%\metis-agent\bin`, and adds
that directory to the user `PATH`. `-Backend none`, `-Model NAME`, `-NoModel`,
`-InstallDir DIR`, `-Version TAG`, `-DryRun`, and `-Uninstall` are supported;
`install.ps1 -Help` lists them all. Native `windows/arm64` builds are not
published yet.

Useful flags on Linux/macOS: `--backend ollama|llamacpp|lmstudio|none`,
`--model NAME`, `--no-model`, `--prefix DIR`, `--version TAG`, `--yes`,
`--dry-run`. `scripts/install.sh --help` lists them all.

The installer writes:

| Path | Purpose |
|---|---|
| `~/.local/share/metis-agent/bin/metis-agent` | the binary (Linux; `~/Library/Application Support/metis-agent` on macOS; `%LOCALAPPDATA%\metis-agent\bin` on Windows) |
| `~/.local/bin/metis-agent` (and `Metis-Agent`) | launcher that starts the backend on demand |
| `~/.config/metis-agent/config.json` (Linux) / `~/Library/Application Support/metis-agent/config.json` (macOS) / `%APPDATA%\metis-agent\config.json` (Windows) | provider, endpoint, and model |
| `~/.local/share/metis-agent/backend.log` | backend logs |

It also adds `~/.local/bin` to `PATH` in your shell rc file on first run, so
`metis-agent` works from any directory — including your home folder. Re-running
the installer upgrades the binary and backs up an existing config first.

To uninstall, use the installer instead of deleting files by hand:

```sh
curl -fsSL https://raw.githubusercontent.com/4matthedev/metis-agent/main/scripts/install.sh | bash -s -- --uninstall
```

```powershell
.\install.ps1 -Uninstall
```

That removes the binary, launcher, and PATH entry but keeps config, models,
sessions, and undo history. Add `--purge` (or `-Purge` on Windows) to remove
those too. Ollama, llama.cpp, and LM Studio are always left installed.

## Requirements

- Go 1.25+ (only when building from source; the installer downloads a release)
- Linux, macOS, or Windows 10 1803+ (Windows uses `scripts/install.ps1`)
- A running OpenAI-compatible model server with streaming tool/function calling:
  - llama-prism (`http://127.0.0.1:9931`), LM Studio (`:1234`), or Ollama (`:11434`)
  - or a hosted provider such as OpenAI, OpenRouter, Groq, or Together
- At least one model **loaded** on local servers, or pass `--model`.
  `--list-models` discovers reachable providers and their models.

## Build

```sh
cd metis-agent
go build -o metis-agent .
```

The TUI uses [Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Bubbles](https://github.com/charmbracelet/bubbles), and
[Lip Gloss](https://github.com/charmbracelet/lipgloss). Go fetches them on
the first build; the result is still one self-contained binary.

## Run

```sh
./metis-agent                              # full-screen TUI
./metis-agent --plain                      # line-based REPL
./metis-agent --once "What time is it?"    # single shot
```

The TUI is used automatically when stdin/stdout are terminals. Piped input
or `--plain` falls back to the REPL, which is handy for scripts.

### TUI keys

| Key | Action |
|---|---|
| `Enter` | Send the current message |
| `Alt+Enter`, `Ctrl+J` | Insert a newline |
| `Ctrl+P` / `Ctrl+N` | Previous / next message in input history |
| `PgUp` / `PgDn` | Scroll the transcript (mouse wheel works too) |
| `Ctrl+O` | Expand / collapse tool output |
| `Esc`, `Ctrl+C` | Cancel the running task |
| `Ctrl+L` | Clear the transcript |
| `Ctrl+R` | Clear the transcript and forget the conversation |
| `Ctrl+C` (idle), `Ctrl+D` | Quit |
| `y` / `a` / `A` / `n` | Answer an approval prompt |
| `↑` / `↓` | Choose a command when the `/` menu is open |
| `Tab`, `Enter` | Complete the selected command (Tab also cycles the operating mode when the input is empty) |

### Slash commands

Type `/` to open the command menu. It lists every command and filters as you
type; use `↑`/`↓` to choose and `Tab` or `Enter` to complete, then `Enter` to
run. `Esc` dismisses the menu.

| Command | Action |
|---|---|
| `/help` | Show commands and keys |
| `/clear` | Clear the transcript |
| `/reset` | Clear the transcript and conversation memory |
| `/status` | Show provider, model, project, and approval mode |
| `/tools` | List the available tools |
| `/expand` | Toggle full tool output |
| `/session` | Show the current session and its file |
| `/sessions` | List recent sessions for this directory |
| `/new` | Start a fresh session |
| `/resume [id]` | Resume the latest (or named) session |
| `/autoapprove [on\|off]` | Toggle or set automatic approval |
| `/approve-edits [on\|off]` | Review in-project file edits with a diff before writing |
| `/model [name]` | List available models or switch the active model |
| `/thinking [level]` | Set reasoning effort (`none`/`low`/`medium`/`high`/`xhigh`) |
| `/compact` | Summarize the conversation and free context |
| `/context` | Show message and approximate token usage |
| `/undo` | Restore files changed by the previous turn |
| `/commit [message]` | Stage everything and commit (generates a message) |
| `/mcp [subcommand]` | Manage MCP servers: list/add/remove/reconnect (see below) |
| `/skills [subcommand]` | Manage the skill system: list/info/use/run (see below) |
| `/mode [name]` | Switch operating mode, or press Tab to cycle (see Modes below) |
| `/quit` | Leave Metis |

`/mcp` subcommands:

- `/mcp` or `/mcp list` — list configured MCP servers and their tools.
- `/mcp add {json}` — add a server, e.g.
  `/mcp add {"type":"stdio","name":"github","command":"npx","args":["-y","@modelcontextprotocol/cli"]}`
  or `/mcp add {"type":"http","transport":"streamable","url":"https://mcp.example.com/mcp"}`.
- `/mcp remove <name>` — disconnect and remove a server.
- `/mcp reconnect <name>` — drop and re-establish a connection.

Servers are persisted to `./.metis-agent.json` and reconnected on startup.

`/skills` subcommands (the `skills` **tool** the model can also call):

- `/skills` or `/skills list` — list skills (from `<project>/.metis-agent/skills/`, `$METIS_SKILL_PATHS`, and `~/.agents/skills/`).
- `/skills info <name>` — show a skill's full body.
- `/skills use <name>` — load a skill's body into context for the rest of the task.
- `/skills run <name>` — execute the skill's declared `run` command. This is **approval-gated** (the user confirms each run, exactly like `shell`). A skill is a `SKILL.md` file with a `name`, `description`, an optional `run` command, and a markdown body.

The plain REPL supports `/help`, `/status`, `/tools`, `/reset`,
`/autoapprove`, `/approve-edits`, `/model`, `/compact`, `/context`, `/undo`,
`/commit`, `/mcp`, and `/skills`; entering `/` alone prints the command list.

Each prompt keeps the previous turns in the conversation (bounded by
`--window`), so follow-up questions work. `/reset` starts fresh.

## Modes

Metis can operate in six postures, each of which rewrites the **system prompt**
sent to the model on every turn (the shared tool list and core rules are
preserved; a mode-specific section is appended):

| Mode | Posture |
|---|---|
| **General** (default) | All-purpose: explore, plan, build, and verify end-to-end. |
| **Planner** | Analyze and plan only — explore the codebase and propose approaches; does not modify code. |
| **Builder** | Implement and refine code — edit, create, and verify precisely. |
| **Debugger** | Diagnose and fix failures — reproduce, isolate the root cause, make the smallest fix. |
| **Cybersecurity** | Find and harden against vulnerabilities — audit code and report findings with severity and fixes. |
| **Simplifier** | Reduce complexity without changing behaviour — remove dead code, consolidate duplicates. |

Switch modes with the **Tab** key (when the input is empty) to cycle through
them, or with `/mode <name>` (`general`, `planner`, `builder`, `debugger`,
`cybersecurity`, `simplifier`). The active mode is shown in the status bar and
confirmed as a transcript line on each switch.

**Planner is enforced, not just suggested.** In Planner mode the registry hides
and refuses every mutating tool — `shell`, the file-writing tools,
`download_file`, non-GET `http_fetch`, external MCP tools, and the skills `run`
action — even when `--auto-approve` is on. The allowlist lives in
`internal/agent/modes.go`.

## Approvals

Metis does not confine tools to a filesystem sandbox. Relative paths resolve
against `--dir`, and absolute paths, `~`, `..`, and symlinks are allowed. To
keep that power safe, the tool registry asks before any call that could be
destructive or reach outside the project:

- every `shell` command,
- `file_delete` (and any other deletion),
- `download_file`,
- non-`GET`/`HEAD` `http_fetch`,
- non-dry-run `replace_in_files`,
- `env_get` with `show_secrets=true`,
- `diagnostics` (it runs build and lint programs),
- reads or writes outside the project directory.

Run with `--approve-edits` (or `/approve-edits on`) to also review every
in-project `file_write`, `file_edit`, and `multi_edit` with a unified diff
before it is written. The diff is computed without touching disk and appears
in the approval block, so you see the exact change rather than a summary.

Tool output is passed through a credential redactor by default. It masks PEM
private keys, bearer tokens, `api_key`/`password`/`secret` assignments, and
well-known token shapes (`sk-…`, `ghp_…`, `AKIA…`, `xox…`, `glpat-…`). Disable
it with `--no-redact`, `METIS_REDACT_SECRETS=false`, or
`"redact_secrets": false` when you are debugging a redaction false positive.

In the TUI an approval block appears above the input:

| Key | Decision |
|---|---|
| `y` | Allow this call once |
| `a` | Always allow this tool for the session |
| `A` | Auto-approve every dangerous call for the session |
| `n`, `Esc` | Deny the call and stop the step |

The plain REPL prompts on stdin with the same choices. Denied calls are
reported to the model as an error, and the model is told not to retry it.

Dangerous calls can be allowed up front with `--auto-approve` (also `-y` or
`--yes`), the `METIS_AUTO_APPROVE=true` environment variable, or
`"auto_approve": true` in a config file. You can also toggle it at runtime with
`/autoapprove`. In non-interactive runs (`--once` with piped stdin, or a piped
REPL) dangerous tools are denied unless auto-approval is enabled.

## Undo, verification, and context

**Undo.** Before a mutating tool changes a path, the registry snapshots its
previous state. Each user turn is one checkpoint; `/undo` or `metis-agent
--undo` restores the most recent one (new files are removed, deleted files and
directories come back). Checkpoints live under the platform data directory
(`.../metis-agent/checkpoints/<project-hash>`) and the last 30 per project are
kept. Very large files and turns that exceed the snapshot budget are reported
as skipped instead of silently ignored.

**Automatic verification.** Set `verify` (or `--verify`) to a command that
proves the change works, for example `go test ./...` or `npm test`. It runs only
after a turn that actually changed files. A failure is fed back to the model as
a new instruction so it can repair its work, up to two attempts; the final
failure is then shown to you. `--verify-timeout` bounds each run.

**Context.** `/context` shows message and rough token usage against `--window`.
`/compact` asks the model to summarize the session and replaces the history
with that summary, so long sessions can continue without losing the decisions,
files, and next step.

## Updating, diagnostics, and editors

```sh
metis-agent --check-update   # is a newer release available?
metis-agent --update         # download, verify SHA256SUMS, replace in place
metis-agent --doctor         # config, data dir, instructions, git, provider, models
```

`--update` only accepts the release asset that matches the running OS and
architecture, verifies it against the release's `SHA256SUMS`, and replaces the
binary atomically. Builds from source report their injected version; the
release workflow sets it with `-ldflags "-X main.version=<tag>"`.

For editors and scripts, `--json` streams one JSON object per line on stdout
(`step`, `text`, `tool_call`, `tool_result`, `done`, `error`) while human-facing
notices stay on stderr. Combine it with `--once` for a request/response
adapter, or with `--plain` for an interactive stream.

`--acp` turns Metis into an [Agent Client Protocol](https://agentclientprotocol.com)
server over stdin/stdout, so an editor can drive it directly. It implements
`initialize`, `session/new`, `session/prompt`, `session/cancel`,
`session/set_mode`, `session/close`, and streams `session/update`
notifications for text and tool calls. Tool approvals use
`session/request_permission`, including the diff preview from `--approve-edits`.
`sessions` are independent agents with their own working directory. Not yet
implemented: `session/load`, client `fs/*` and `terminal/*` methods, and
per-session MCP servers (Metis uses its configured MCP servers).

**Semantic search.** Set an embedding model to add the `semantic_search` tool:

```sh
ollama pull nomic-embed-text
metis-agent --embedding-model nomic-embed-text
```

The repository is chunked, embedded through the provider's OpenAI-compatible
`/v1/embeddings` endpoint, and cached under the data directory. The cache is
keyed by the model and invalidated when file sizes or timestamps change; pass
`rebuild=true` to the tool after large edits. Without an embedding model the
tool is not offered to the model. This works with any provider that exposes
`/v1/embeddings`.

## Flags

| Flag | Default | Description |
|---|---|---|
| `--provider` | `auto` | `auto`, `llama-prism`, `lmstudio`, `ollama`, `openai`, `openrouter`, `groq`, `together`, `compatible` |
| `--base-url` | preset | OpenAI-compatible base URL |
| `--api-key` | env | API key (prefer provider env vars) |
| `--model` | auto | Model name; discovered when a provider has one sensible choice |
| `--port` | — | Legacy shortcut for `http://127.0.0.1:PORT` |
| `--temp` | `0.3` | Temperature |
| `--max-steps` | `40` | Max model/tool iterations per task |
| `--window` | `64` | Conversation messages kept in context |
| `--system` | Metis prompt | Override the system prompt |
| `--dir` | `.` | Working dir for tools and shell |
| `--no-shell` | `false` | Disable the `shell` tool |
| `--shell-timeout` | `120` | Default shell timeout in seconds (max 900) |
| `--auto-approve`, `-y`, `--yes` | `false` | Run dangerous tool calls without asking |
| `--approve-edits` | `false` | Review in-project file edits with a diff before writing |
| `--redact-secrets`, `--no-redact` | on | Redact credentials in tool output |
| `--verify CMD` | — | Run CMD automatically after a turn that changed files |
| `--verify-timeout N` | `120` | Auto-verify timeout in seconds |
| `--fallback-models L` | — | Comma-separated models to try when the active one fails |
| `--instructions L` | — | Comma-separated extra instruction files |
| `--embedding-model M` | — | Embedding model that enables `semantic_search` (for example `nomic-embed-text`) |
| `--highlight` | `auto` | `auto`, `dark`, `light`, or `off` |
| `--config` | — | Load a specific config file |
| `--no-config` | `false` | Ignore config files |
| `--init-config` | `false` | Write a starter config file and exit |
| `--list-models` | `false` | List models from reachable providers and exit |
| `--session NAME` | — | Load or create a named session |
| `--resume` | `false` | Resume the latest session for `--dir` |
| `--no-session` | `false` | Disable session persistence |
| `--plain` | `false` | Disable the TUI and use the line REPL |
| `--json` | `false` | Stream NDJSON events on stdout (implies non-TUI) |
| `--acp` | `false` | Run as an Agent Client Protocol server over stdin/stdout |
| `--once TASK` | — | Single-shot mode (no TUI) |
| `--doctor` | `false` | Diagnose the installation and model backend |
| `--update` | `false` | Download and install the latest release |
| `--check-update` | `false` | Report whether a newer release exists |
| `--undo` | `false` | Restore files changed by the previous turn |
| `--version` | — | Print the version and exit |
| `-h`, `--help` | — | Show help |

### Configuration

Settings can come from a global config, a project config, environment
variables, and flags, in that order (later wins). Run `--init-config` to write
a starter file. Environment variables use the `METIS_` prefix, for example
`METIS_PROVIDER`, `METIS_MODEL`, `METIS_AUTO_APPROVE`,
`METIS_SHELL_TIMEOUT_SEC`, `METIS_REDACT_SECRETS`, `METIS_VERIFY`,
`METIS_VERIFY_TIMEOUT_SEC`, `METIS_INSTRUCTIONS`, `METIS_FALLBACK_MODELS`,
`METIS_THINKING`, and `METIS_EMBEDDING_MODEL`. See `internal/config/config.go`
for the full set.

Notable fields:

| Field | Default | Meaning |
|---|---|---|
| `redact_secrets` | `true` | Mask credentials in tool output |
| `verify` | `""` | Command to run after a turn changes files (for example `go test ./...`) |
| `verify_timeout_sec` | `120` | Timeout for that command |
| `instructions` | `[]` | Extra instruction files to load into the system prompt |
| `fallback_models` | `[]` | Models to try in order when the active model fails |
| `thinking` | `""` | Thinking level (reasoning effort): `none`, `low`, `medium`, `high`, `xhigh` |
| `embedding_model` | `""` | Embedding model for `semantic_search` (empty disables the tool) |

Instruction files are loaded from the global config directory (`AGENTS.md`,
`METIS.md`, `rules.md`), then the project (`AGENTS.md`, `METIS.md`,
`CLAUDE.md`, `.metis-agent/rules.md`, `.metis-agent/instructions.md`), then any
paths listed in `instructions`. They are appended to the system prompt with
their path as a heading.

The project config is `./.metis-agent.json` (the global file is
`$XDG_CONFIG_HOME/metis-agent/config.json`, or the platform equivalent).
Copy `.metis-agent.example.json` as a starting point:

```sh
cp .metis-agent.example.json .metis-agent.json
```

It can contain `api_key` and MCP `auth_token` values, so it is git-ignored by
default — commit only the example, never real credentials. MCP servers go in
its `mcp_servers` array (or the `METIS_MCP_SERVERS` environment variable).

## Tools

| Tool | Args | What it does |
|---|---|---|
| `time` | — | Current local date/time (ISO 8601) |
| `list_dir` | `path`, `depth`, `include_hidden`, `max_entries` | Explore a directory tree; skips `.git`, `node_modules`, etc. |
| `glob` | `pattern`, `path`, `include_hidden`, `max_results` | Find files by glob (`**/*.go`, `src/**/*.ts`, …) |
| `project_info` | `path`, `max_files`, `include_hidden` | Detect languages, manifests, git branch, file counts, and layout |
| `file_read` | `path`, optional `start_line`/`end_line` | Read a text file or a line range |
| `file_read_many` | `paths`, optional `start_line`/`end_line` | Read several related files in one call |
| `file_stat` | `path` | Type, size, mode, modification time, and line count |
| `search` | `pattern`, `path`, `include`, `max_results` | Regex-search file contents and return `path:line: text` |
| `hash_file` | `path`, `algorithm`, `expected` | Compute or verify MD5/SHA-1/SHA-256/SHA-512 digests |
| `json_query` | `path`/`json`, `query`/`queries`, `indent` | Extract dot/bracket paths from JSON |
| `file_edit` | `path`, `old_text`, `new_text`, `replace_all` | Precise replacement in an existing file |
| `multi_edit` | `path`, `edits` | Apply several related replacements atomically |
| `replace_in_files` | `pattern`, `replacement`, `path`, `include`, `literal`, `dry_run` | Regex/literal replacement across many files |
| `file_write` | `path`, `content` | Create or fully overwrite a file |
| `make_dir` | `path` | Create a directory tree |
| `file_move` | `from`, `to`, `overwrite` | Move or rename a file or directory |
| `file_copy` | `from`, `to`, `overwrite` | Copy a file |
| `file_delete` | `path`, `recursive` | Delete a file or directory |
| `diff_files` | `a`, `b`, `context` | Unified diff between two files |
| `git_status` | `path` | Working tree status with branch information |
| `git_diff` | `path`, `file`, `staged`, `stat` | Unstaged or staged git diffs |
| `git_log` | `path`, `file`, `limit`, `all`, `author`, `full` | Recent commits |
| `git_show` | `rev`, `path`, `file`, `stat` | Show a commit, tag, or object |
| `git_blame` | `file`, `path`, `start_line`, `end_line` | Line-by-line authorship |
| `http_fetch` | `url`, `method`, `headers`, `body`, `json`, `timeout_sec`, `max_bytes` | Make HTTP(S) requests and read text/JSON/HTML |
| `web_search` | `query`, `max_results` | Search the public web without an API key |
| `download_file` | `url`, `path`, `overwrite`, `timeout_sec`, `max_bytes` | Download a URL and write it into a file atomically |
| `repo_map` | `path`, `include`, `max_files`, `max_symbols` | Compact symbol map (functions, types, classes) of the repository |
| `semantic_search` | `query`, `path`, `max_results`, `rebuild` | Meaning-based search over a cached embedding index (requires `--embedding-model`) |
| `diagnostics` | `path`, `commands`, `timeout_sec`, `stop_on_failure` | Run detected build, type-check, and lint commands |
| `subagent` | `task` | Delegate an independent read-only investigation to a fresh agent |
| `system_info` | — | OS, architecture, CPU count, user, temp path, and time |
| `env_get` | `name`/`prefix`, `max_entries`, `show_secrets` | Read environment variables with secret redaction |
| `which` | `command`/`commands` | Locate executables on PATH |
| `port_check` | `host`, `port`/`ports`, `timeout_ms` | Check whether TCP ports accept connections |
| `todo_write` | `items` | Replace the task list for multi-step work |
| `todo_read` | — | Read the current task list |
| `shell` | `command`, `cwd`, `timeout_sec` | Run a non-interactive `bash -c` command |

The agent is told to explore first, plan multi-step work with `todo_write`, use
`repo_map` to orient itself in a large codebase, use `file_edit` or `multi_edit`
for small changes, use the filesystem and git tools instead of shell where
possible, and verify with `diagnostics` (or builds/tests via `shell`). Broad
mechanical refactors can use `replace_in_files` with `dry_run=true` first.
`subagent` runs a separate read-only agent with its own context, which keeps
broad research out of the main conversation.

**Path access and approval:** relative paths resolve against `--dir`, while
absolute paths, `~`, `..`, and symlinks are allowed. Operations outside the
project are not blocked, but they prompt for approval first (see
[Approvals](#approvals)). Tool failures are returned to the model as plain text
so it can correct the argument or explain the failure.

**Shell:** commands start in `--dir` and can read or modify anything your user
account can access, including the network. Every shell call requires approval
unless auto-approval is enabled; use `--no-shell` when you want file tools only.
Shell output is ANSI-cleaned and capped (head + tail), and long commands can be
cancelled with `Esc`/`Ctrl+C`.

## MCP servers

Metis can connect to MCP (Model Context Protocol) servers and expose their tools
as regular Metis tools. Each server's tools are namespaced as `<server>__<tool>`
so they don't collide with the built-in set.

Because an MCP tool runs on a server Metis does not control, every MCP tool call
requires the same approval prompt as a `shell` call (auto-approved only when
`auto_approve` is on or with `--yes`). A failed connection is logged as a warning
and the agent runs with the remaining tools.

Config (in the `mcp_servers` array of a config file, or `METIS_MCP_SERVERS`):

```json
{
  "mcp_servers": [
    {
      "name": "github",
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/cli"],
      "env": { "GITHUB_TOKEN": "..." }
    },
    {
      "name": "http-remote",
      "type": "http",
      "transport": "streamable",
      "url": "https://mcp.example.com/mcp",
      "auth_type": "bearer",
      "auth_token": "...",
      "headers": { "X-Client": "metis" }
    }
  ]
}
```

`type` is `stdio` (default, requires `command`) or `http` (requires `url`, with
`transport` `streamable` (default) or `sse`). `name` is optional and is derived
from the command path or URL host when omitted. Server tools appear in the TUI
and via `/tools` prefixed with their server name.

## Providers

`--provider auto` (the default) probes llama-prism, LM Studio, and Ollama. If
a local provider has exactly one loaded model, that model is selected;
otherwise pass `--model`. Hosted providers read their usual API key
environment variable (`OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `GROQ_API_KEY`,
`TOGETHER_API_KEY`) and can also use `--api-key`.

```sh
./metis-agent --list-models
./metis-agent --provider ollama --model MODEL
./metis-agent --provider lmstudio --model MODEL
./metis-agent --provider openai --model MODEL
./metis-agent --base-url http://127.0.0.1:9931 --model MODEL
./metis-agent --provider compatible --base-url https://example.com/v1 --model MODEL
```

If a local server reports an unknown or unloaded model, metis-agent stops with
a clear `MODEL NOT LOADED` message instead of a raw HTTP dump.

## Examples

```sh
./metis-agent
./metis-agent --plain
./metis-agent --once "What time is it?"
./metis-agent --auto-approve --once "Run the tests and fix them."
./metis-agent --dir /tmp/code --once "Fix the failing tests and rerun them."
./metis-agent --dir /tmp/code --no-shell --once "Summarize the repo."
./metis-agent --once "Write 'hello metis' to note.txt, then read it back."
./metis-agent --dir /tmp/metis-demo --once "Create report.txt with today's date."
./metis-agent --model does-not-exist --once "hello"   # MODEL NOT LOADED
./metis-agent --max-steps 3 --once "Do something complicated"
./metis-agent --verify "go test ./..." --once "Fix the failing tests."
./metis-agent --approve-edits --once "Rename the helper and update callers."
./metis-agent --json --once "Summarize the repository."
./metis-agent --doctor
./metis-agent --help
```

## Project layout

```
metis-agent/
  go.mod
  main.go                         # CLI flags, TUI/REPL/one-shot entrypoints
  features.go                     # JSON events, undo/commit/verify, subagent wiring
  agent_build.go                  # shared agent construction for CLI and ACP sessions
  scripts/install.sh              # Linux/macOS installer
  scripts/install.ps1             # native Windows installer
  internal/tui/tui.go             # Bubble Tea model, input, events, layout
  internal/tui/styles.go          # palette and transcript rendering
  internal/llm/client.go          # OpenAI-compatible client, SSE streaming
  internal/llm/router.go          # runtime model switching + fallback chain
  internal/agent/agent.go         # agent loop + progress events
  internal/memory/history.go      # history + validated sliding window
  internal/tools/registry.go      # tool registry + orchestration
  internal/tools/approval.go      # dangerous-action classification and approval state
  internal/tools/approval_preview.go # diffs for the edit approval prompt
  internal/tools/tool.go          # tool types, schemas, path helpers
  internal/tools/file.go          # read/write/edit/list/search tools
  internal/tools/fs.go            # glob, batch read, stat, refactor, hash, move/copy/delete
  internal/tools/project.go       # project detection and repository summary
  internal/tools/repomap.go       # symbol map across languages
  internal/tools/semantic.go      # semantic_search tool
  internal/tools/diagnostics.go   # detected build/type-check/lint commands
  internal/tools/websearch.go     # keyless web search
  internal/tools/subagent.go      # read-only delegated research tool
  internal/tools/git.go           # status, diff, log, show, blame
  internal/tools/json.go          # JSON path queries
  internal/tools/system.go        # host info, env, which, port checks
  internal/tools/todo.go          # in-session task list
  internal/tools/web.go           # HTTP fetch and atomic downloads with SSRF guards
  internal/tools/shell.go         # shell tool: execution, timeout, output cap
  internal/config/config.go       # config file, env, and platform paths
  internal/checkpoint/            # per-turn file snapshots for /undo
  internal/embeddings/            # cached OpenAI-compatible embedding index
  internal/acp/                   # Agent Client Protocol server (--acp)
  internal/instructions/          # AGENTS.md/METIS.md/rules loading
  internal/redact/                # credential redaction for tool output
  internal/update/                # GitHub release check/download/checksum/replace
  internal/doctor/                # installation diagnostics
  internal/session/session.go     # per-project session persistence
```

## How the agent loop works

1. The agent sends the system prompt, recent history, and tool schemas to
   the server with `stream: true`.
2. Text deltas are forwarded to the UI immediately.
3. If the model requests tools, each one runs in registration order with the
   run context (so cancellation stops a long command); the results are
   appended as `tool` messages and the model is asked again.
4. When the model answers without tool calls, the turn is finished. If the
   turn changed files and `verify` is configured, Metis runs it and feeds a
   failure back as a new instruction (up to two repair attempts).
5. A sliding window trims old messages without ever leaving orphaned tool
   results or half-completed tool-call groups in the prompt. `/compact` can
   replace the history with a model-written summary when a session gets long.

The client also accepts servers that ignore `stream: true` and return a
single JSON completion.

## Development

```sh
make check   # gofmt + vet + build + tests
make race    # tests with the race detector
make cover   # coverage summary
```

CI runs `gofmt`, `go vet`, `go build`, `go test -race`, and cross-builds for
macOS and Windows.

## License

MIT — see [LICENSE](LICENSE).
