# Metis Agent

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go 1.25+">
  <img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="MIT License">
  <img src="https://img.shields.io/badge/Local--First-AI-7c3aed" alt="Local First AI">
</p>

```text
███╗   ███╗███████╗████████╗██╗███████╗    █████╗  ██████╗ ███████╗███╗   ██╗████████╗
████╗ ████║██╔════╝╚══██╔══╝██║╔════╝     ██╔══██╗██╔════╝ ██╔════╝████╗  ██║╚══██╔══╝
██╔████╔██║█████╗     ██║   ██║███████╗   ███████║██║  ███╗█████╗  ██╔██╗ ██║   ██║
██║╚██╔╝██║╔══╝     ██║   ██║╚════██║   ██╔══██║██║   ██║██╔══╝  ██║╚██╗██║   ██║
██║ ╚═╝ ██║███████╗   ██║   ██║███████║   ██║  ██║╚██████╔╝███████╗██║ ╚████║   ██║
╚═╝     ╚═╝╚══════╝   ╚═╝   ╚═╝╚══════╝   ╚═╝  ╚═╝ ╚═════╝ ╚══════╝╚═╝  ╚═══╝   ╚═╝
```

**An autonomous coding agent. Your models. Your machine. Your codebase.**

Metis is a Go-based coding agent that explores repositories, understands code, edits files, runs commands, investigates failures, and verifies changes. Use a local model or connect to a hosted OpenAI-compatible provider — without tying your workflow to a single model or platform.

Work with Metis through a full-screen terminal interface, a plain REPL, a one-shot command, or the Agent Client Protocol.

> **Give Metis a task. It explores, plans, builds, and verifies.**

---

## Why Metis?

A coding agent should do more than generate code. It should understand the project it is working in, use the right tools, make deliberate changes, and check whether those changes actually work.

Metis brings that workflow into one configurable, terminal-native agent.

- **Local-first by design.** Run against local model servers such as Ollama, LM Studio, or llama.cpp-compatible endpoints.
- **Model freedom.** Switch between local and hosted OpenAI-compatible providers.
- **Tool-driven development.** Explore files, inspect Git history, edit code, run tests, and investigate failures.
- **Multiple working modes.** Plan, build, debug, audit, or simplify with purpose-specific agent modes.
- **A workflow you can inspect.** See tool activity, review changes, approve sensitive operations, and undo previous turns.
- **Built for the terminal.** Use the interactive TUI, plain REPL, one-shot execution, or ACP integration.

Metis is designed to keep the development workflow close to your codebase — and your control.

## See it in action

A typical task: ask Metis to fix a failing test. It investigates the project, reproduces the failure, edits the relevant file, and runs the tests again.

```text
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

*Illustrative terminal session.*

---

## Install

Get a working local setup with the installer.

### Quick install — Linux and macOS

Easy mode installs Ollama, pulls `gemma4:e2b`, installs Metis, and configures the environment:

```bash
curl -fsSL https://raw.githubusercontent.com/4matthedev/metis-agent/main/scripts/install.sh | bash
```

### Advanced install

Choose a backend and configure the model yourself. The installer supports Ollama, llama.cpp, and LM Studio. It reuses an available model and, when none is found, selects a Gemma variant based on detected RAM (`gemma4:e2b` below 16 GB, `gemma4:e4b` above):

```bash
curl -fsSL https://raw.githubusercontent.com/4matthedev/metis-agent/main/scripts/install.sh | bash -s -- --advanced
```

### Windows

Run the installer from PowerShell (Windows 10 1803+ for bundled `tar.exe`):

```powershell
irm https://raw.githubusercontent.com/4matthedev/metis-agent/main/scripts/install.ps1 | iex
```

The Windows installer:
- Installs Ollama through `winget` when needed.
- Pulls `gemma4:e2b` unless configured otherwise.
- Verifies the release checksum.
- Installs to `%LOCALAPPDATA%\metis-agent\bin`.
- Adds the install directory to the user `PATH`.

Supported options include `-Backend none`, `-Model NAME`, `-NoModel`, `-InstallDir DIR`, `-Version TAG`, `-DryRun`, and `-Uninstall`. Run `install.ps1 -Help` for the full list.

Native `windows/arm64` builds are not published yet.

### Installer options

Linux/macOS installer flags:

| Flag | Description |
|---|---|
| `--backend ollama\|llamacpp\|lmstudio\|none` | Select the model backend |
| `--model NAME` | Select a model |
| `--no-model` | Skip model installation |
| `--prefix DIR` | Choose an install prefix |
| `--version TAG` | Install a specific release |
| `--yes` | Run without interactive confirmation |
| `--dry-run` | Preview installer actions |

Run `scripts/install.sh --help` for all options.

### Installed files

| Path | Purpose |
|---|---|
| `~/.local/share/metis-agent/bin/metis-agent` | Binary on Linux |
| `~/Library/Application Support/metis-agent` | Application data on macOS |
| `%LOCALAPPDATA%\metis-agent\bin` | Binary on Windows |
| `~/.local/bin/metis-agent` | Linux launcher |
| `~/.config/metis-agent/config.json` | Linux configuration |
| `~/Library/Application Support/metis-agent/config.json` | macOS configuration |
| `%APPDATA%\metis-agent\config.json` | Windows configuration |
| `~/.local/share/metis-agent/backend.log` | Backend logs |

On first run, the Linux/macOS installer adds `~/.local/bin` to the shell rc file when needed. Re-running the installer upgrades the binary and backs up an existing configuration.

### Uninstall

Use the installer to uninstall Metis.

Linux/macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/4matthedev/metis-agent/main/scripts/install.sh | bash -s -- --uninstall
```

Windows:

```powershell
.\install.ps1 -Uninstall
```

Uninstalling removes the binary, launcher, and PATH entry, but keeps configuration, models, sessions, and undo history. Add `--purge` (or `-Purge` on Windows) to remove those as well.

Ollama, llama.cpp, and LM Studio are left installed.

---

## Requirements

- Go 1.25+ only when building from source. The installer downloads a release.
- Linux, macOS, or Windows 10 1803+.
- A running OpenAI-compatible model server with streaming tool/function calling.
- At least one model loaded on local servers, or a model specified with `--model`.

Supported local endpoints include:

| Backend | Typical endpoint |
|---|---|
| llama-prism | `http://127.0.0.1:9931` |
| LM Studio | `http://127.0.0.1:1234` |
| Ollama | `http://127.0.0.1:11434` |

Metis also supports hosted providers such as OpenAI, OpenRouter, Groq, and Together, along with compatible endpoints.

Use `--list-models` to discover models from reachable providers.

## Build from source

```bash
git clone https://github.com/4matthedev/metis-agent.git
cd metis-agent
go build -o metis-agent .
```

The TUI uses [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Bubbles](https://github.com/charmbracelet/bubbles), and [Lip Gloss](https://github.com/charmbracelet/lipgloss). Dependencies are fetched by Go during the first build. The result is a single self-contained binary.

## Run Metis

```bash
./metis-agent                              # Full-screen TUI
./metis-agent --plain                      # Line-based REPL
./metis-agent --once "What time is it?"    # One-shot execution
```

Metis launches the TUI automatically when stdin and stdout are terminals. Piped input or `--plain` uses the REPL, which is useful for scripts and automation.

---

## Choose how you work

Metis supports several ways to interact with the same agent workflow.

| Interface | Best for |
|---|---|
| **TUI** | Interactive development and visible tool activity |
| **Plain REPL** | Terminal workflows and scripting |
| **One-shot** | Single tasks and automation |
| **JSON events** | Editor and external integrations |
| **ACP** | Editors that support the Agent Client Protocol |

### TUI keyboard shortcuts

| Key | Action |
|---|---|
| `Enter` | Send the current message |
| `Alt+Enter`, `Ctrl+J` | Insert a newline |
| `Ctrl+P` / `Ctrl+N` | Previous / next input-history message |
| `PgUp` / `PgDn` | Scroll the transcript |
| `Ctrl+O` | Expand / collapse tool output |
| `Esc`, `Ctrl+C` | Cancel a running task |
| `Ctrl+L` | Clear the transcript |
| `Ctrl+R` | Clear the transcript and forget the conversation |
| `Ctrl+C` (idle), `Ctrl+D` | Quit |
| `y` / `a` / `A` / `n` | Respond to an approval prompt |
| `↑` / `↓` | Navigate the command menu |
| `Tab`, `Enter` | Complete the selected command |
| `Tab` (empty input) | Cycle operating modes |

### Slash commands

Type `/` to open the command menu. It filters as you type. Use `↑` and `↓` to select a command, then `Tab` or `Enter` to complete it.

| Command | Action |
|---|---|
| `/help` | Show commands and keys |
| `/clear` | Clear the transcript |
| `/reset` | Clear the transcript and conversation memory |
| `/status` | Show provider, model, project, and approval mode |
| `/tools` | List available tools |
| `/expand` | Toggle full tool output |
| `/session` | Show the current session and its file |
| `/sessions` | List recent sessions for this directory |
| `/new` | Start a fresh session |
| `/resume [id]` | Resume the latest or a named session |
| `/autoapprove [on\|off]` | Toggle automatic approval |
| `/approve-edits [on\|off]` | Review in-project file edits before writing |
| `/model [name]` | List models or switch the active model |
| `/thinking [level]` | Set reasoning effort |
| `/theme [auto\|dark\|light]` | Switch syntax-highlighting palette |
| `/compact` | Summarize the conversation and free context |
| `/context` | Show message and approximate token usage |
| `/undo` | Restore files changed by the previous turn |
| `/commit [message]` | Stage everything and create a commit |
| `/copy` | Copy the last assistant message |
| `/mcp [subcommand]` | Manage MCP servers |
| `/skills [subcommand]` | Manage skills |
| `/mode [name]` | Switch operating mode |
| `/quit` | Leave Metis |

The plain REPL supports `/help`, `/status`, `/tools`, `/reset`, `/autoapprove`, `/approve-edits`, `/model`, `/compact`, `/context`, `/undo`, `/commit`, `/mcp`, and `/skills`. Entering `/` prints the command list.

---

## Six modes. One agent.

Different tasks call for different approaches. Metis provides six operating modes, each adding a mode-specific section to the model's system prompt while preserving the shared tool list and core rules.

| Mode | Purpose |
|---|---|
| **General** | Explore, plan, build, and verify end-to-end. |
| **Planner** | Analyze the codebase and propose an approach without modifying code. |
| **Builder** | Implement, refine, and verify changes. |
| **Debugger** | Reproduce failures, isolate root causes, and make focused fixes. |
| **Cybersecurity** | Audit code for vulnerabilities and report findings with severity and fixes. |
| **Simplifier** | Reduce complexity without changing behavior. |

Switch modes with `Tab` when the input is empty, or use:

```text
/mode general
/mode planner
/mode builder
/mode debugger
/mode cybersecurity
/mode simplifier
```

The active mode appears in the status bar.

**Planner mode is enforced at the tool-registry level.** It hides and refuses mutating tools, including `shell`, file-writing tools, `download_file`, non-GET `http_fetch`, external MCP tools, and the skills `run` action — even when automatic approval is enabled. The allowlist is implemented in `internal/agent/modes.go`.

---

## Tools for real development work

Metis uses tools to interact with the repository and development environment. The model can inspect the project, read related files, search for symbols, make edits, inspect Git history, run diagnostics, and delegate read-only research.

### Built-in tools

| Tool | What it does |
|---|---|
| `time` | Get the current local date and time |
| `list_dir` | Explore directory contents |
| `glob` | Find files by glob pattern |
| `project_info` | Detect languages, manifests, Git branch, file counts, and layout |
| `file_read` | Read a file or line range |
| `file_read_many` | Read several files in one call |
| `file_stat` | Inspect file type, size, mode, modification time, and line count |
| `search` | Search file contents using a regular expression |
| `hash_file` | Compute or verify file digests |
| `json_query` | Extract values from JSON |
| `file_edit` | Replace exact text in an existing file |
| `multi_edit` | Apply several related replacements atomically |
| `replace_in_files` | Replace text across multiple files |
| `file_write` | Create or overwrite a file |
| `make_dir` | Create directories |
| `file_move` | Move or rename files and directories |
| `file_copy` | Copy files |
| `file_delete` | Delete files or directories |
| `diff_files` | Compare files using a unified diff |
| `git_status` | Inspect repository status and branch |
| `git_diff` | Inspect staged or unstaged changes |
| `git_log` | Inspect commit history |
| `git_show` | Inspect commits, tags, or Git objects |
| `git_blame` | Inspect line-by-line authorship |
| `http_fetch` | Make HTTP(S) requests |
| `web_search` | Search the public web without an API key |
| `download_file` | Download files atomically |
| `repo_map` | Build a compact symbol map across languages |
| `semantic_search` | Search the repository by meaning |
| `diagnostics` | Run detected build, type-check, and lint commands |
| `subagent` | Delegate independent, read-only research |
| `system_info` | Inspect host system information |
| `env_get` | Read environment variables with secret redaction |
| `which` | Locate executables on `PATH` |
| `port_check` | Check TCP port availability |
| `todo_write` | Create or replace the current task list |
| `todo_read` | Read the current task list |
| `shell` | Run non-interactive shell commands |

Metis is instructed to explore before making changes, plan multi-step tasks, use repository maps when helpful, prefer focused edits, and verify its work.

For small changes, it can use `file_edit` or `multi_edit`. For broad mechanical refactors, `replace_in_files` supports a dry-run preview.

### Read-only subagents

The `subagent` tool delegates an independent investigation to a fresh agent with its own context. It is read-only and is useful for exploring a large codebase without filling the main agent's conversation with research details.

---

## Control over changes

Autonomy should not mean losing visibility or control. Metis includes approval workflows, credential redaction, checkpoints, and verification options.

### Approval workflow

Metis does not confine tools to a filesystem sandbox. Relative paths resolve against `--dir`; absolute paths, `~`, `..`, and symlinks are allowed.

The tool registry requests approval before operations that may be destructive or reach outside the project:

- Every `shell` command.
- `file_delete` and other deletion operations.
- `download_file`.
- Non-`GET`/`HEAD` `http_fetch`.
- Non-dry-run `replace_in_files`.
- `env_get` with `show_secrets=true`.
- `diagnostics`, which runs build and lint programs.
- Reads or writes outside the project directory.

Use `--approve-edits` or `/approve-edits on` to review every in-project `file_write`, `file_edit`, and `multi_edit` operation with a unified diff before it is written.

The diff is computed without touching disk, so you can inspect the proposed change before approving it.

### Approval choices

| Key | Decision |
|---|---|
| `y` | Allow this call once |
| `a` | Always allow this tool for the session |
| `A` | Auto-approve dangerous calls for the session |
| `n`, `Esc` | Deny the call and stop the step |

The plain REPL uses the same choices.

Dangerous calls can also be allowed up front with `--auto-approve`, `-y`, `--yes`, `METIS_AUTO_APPROVE=true`, or `"auto_approve": true` in the configuration file.

In non-interactive runs, dangerous tools are denied unless automatic approval is enabled.

### Credential redaction

Tool output passes through a credential redactor by default. It masks:

- PEM private keys.
- Bearer tokens.
- `api_key`, `password`, and `secret` assignments.
- Common token formats such as `sk-…`, `ghp_…`, `AKIA…`, `xox…`, and `glpat-…`.

For debugging a false positive, redaction can be configured with `--no-redact`, `METIS_REDACT_SECRETS=false`, or `"redact_secrets": false`.

### Undo and checkpoints

Before a mutating tool changes a path, Metis snapshots its previous state.

Each user turn is one checkpoint. Use `/undo` or `metis-agent --undo` to restore the most recent checkpoint:

- Newly created files are removed.
- Deleted files and directories are restored.
- Previous file contents are recovered.

Checkpoints are stored under the platform data directory in `metis-agent/checkpoints/<project-hash>`. The last 30 checkpoints per project are retained.

Very large files and turns that exceed the snapshot budget are reported as skipped rather than silently ignored.

### Automatic verification

Set `verify` or use `--verify` to run a command after a turn that changed files.

For example:

```bash
./metis-agent --verify "go test ./..."
```

Verification runs only after a turn that actually changed files. If it fails, the failure is sent back to the model so it can attempt a repair. Metis allows up to two repair attempts before showing the final failure.

Use `--verify-timeout` to bound the verification command.

### Context management

Long coding sessions can consume a lot of context.

- `/context` shows message count and approximate token usage against `--window`.
- `/compact` asks the model to summarize the session and replaces the conversation history with that summary.

This helps longer sessions continue while retaining important decisions, files, and next steps.

---

## Extend Metis with MCP

Metis supports the [Model Context Protocol](https://modelcontextprotocol.io/). Connect MCP servers and expose their tools alongside Metis's built-in tools.

MCP tools are namespaced as `<server>__<tool>` to avoid collisions.

Every MCP tool call requires approval, just like a shell call. Automatic approval is available through the same configuration options.

If an MCP server fails to connect, Metis logs a warning and continues with the remaining tools.

### Manage servers

```text
/mcp
/mcp list
/mcp add {"type":"stdio","name":"github","command":"npx","args":["-y","@modelcontextprotocol/cli"]}
/mcp add {"type":"http","transport":"streamable","url":"https://mcp.example.com/mcp"}
/mcp remove github
/mcp reconnect github
```

Servers are persisted in `./.metis-agent.json` and reconnected on startup.

### Configuration example

```json
{
  "mcp_servers": [
    {
      "name": "github",
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/cli"],
      "env": {
        "GITHUB_TOKEN": "..."
      }
    },
    {
      "name": "http-remote",
      "type": "http",
      "transport": "streamable",
      "url": "https://mcp.example.com/mcp",
      "auth_type": "bearer",
      "auth_token": "...",
      "headers": {
        "X-Client": "metis"
      }
    }
  ]
}
```

`type` can be `stdio` (the default, requires `command`) or `http` (requires `url`, with `streamable` or `sse` transport). If omitted, `name` is derived from the command path or URL host.

Server tools appear in the TUI and `/tools`, prefixed with the server name.

---

## Skills

Metis supports a skill system for reusable task-specific instructions.

Skills are `SKILL.md` files with:
- A `name`.
- A `description`.
- An optional `run` command.
- A Markdown body containing the skill instructions.

Skills can be discovered from:

- `<project>/.metis-agent/skills/`
- Paths in `$METIS_SKILL_PATHS`.
- `~/.agents/skills/`

Use the slash commands:

```text
/skills
/skills list
/skills info <name>
/skills use <name>
/skills run <name>
```

`/skills use` loads the skill body into context for the rest of the task.

`/skills run` executes the skill's declared command and requires approval, just like `shell`.

---

## Model providers

Metis is not tied to one model provider. Use a local server, a hosted provider, or another OpenAI-compatible endpoint.

### Provider selection

`--provider auto` is the default. It probes llama-prism, LM Studio, and Ollama.

If a local provider has exactly one loaded model, Metis selects it. Otherwise, specify a model with `--model`.

Hosted providers use their usual environment variables:

| Provider | Environment variable |
|---|---|
| OpenAI | `OPENAI_API_KEY` |
| OpenRouter | `OPENROUTER_API_KEY` |
| Groq | `GROQ_API_KEY` |
| Together | `TOGETHER_API_KEY` |

You can also provide a key with `--api-key`.

### Provider examples

```bash
./metis-agent --list-models

./metis-agent --provider ollama --model MODEL
./metis-agent --provider lmstudio --model MODEL
./metis-agent --provider openai --model MODEL

./metis-agent --base-url http://127.0.0.1:9931 --model MODEL
./metis-agent --provider compatible --base-url https://example.com/v1 --model MODEL
```

If a local server reports an unknown or unloaded model, Metis stops with a clear `MODEL NOT LOADED` message instead of displaying a raw HTTP error dump.

### Semantic search

Metis can add meaning-based repository search when an embedding model is configured.

For example, with Ollama:

```bash
ollama pull nomic-embed-text
metis-agent --embedding-model nomic-embed-text
```

The repository is chunked and embedded through the provider's OpenAI-compatible `/v1/embeddings` endpoint. The index is cached under the data directory.

The cache is keyed by model and invalidated when file sizes or timestamps change. After large edits, pass `rebuild=true` to the tool.

Without an embedding model, `semantic_search` is not offered to the model.

Any provider exposing `/v1/embeddings` can be used.

---

## Editors and automation

Metis can integrate with external workflows through JSON events and the Agent Client Protocol.

### JSON event stream

Use `--json` to stream newline-delimited JSON events to stdout. Human-facing notices remain on stderr.

Events include:

- `step`
- `text`
- `tool_call`
- `tool_result`
- `done`
- `error`

Combine `--json` with `--once` for a request/response adapter, or with `--plain` for an interactive stream.

### Agent Client Protocol

Run Metis as an [Agent Client Protocol](https://agentclientprotocol.com) server:

```bash
./metis-agent --acp
```

The server communicates over stdin/stdout and implements:

- `initialize`
- `session/new`
- `session/prompt`
- `session/cancel`
- `session/set_mode`
- `session/close`

It streams `session/update` notifications for text and tool calls. Tool approvals use `session/request_permission`, including diff previews when `--approve-edits` is enabled.

ACP sessions are independent agents with their own working directory.

Not yet implemented:
- `session/load`
- Client `fs/*` methods.
- Client `terminal/*` methods.
- Per-session MCP servers. Metis uses its configured MCP servers.

---

## Configuration

Metis can load settings from a global config, a project config, environment variables, and command-line flags. Later sources take precedence.

Generate a starter configuration:

```bash
metis-agent --init-config
```

### Configuration locations

Project configuration:

```text
./.metis-agent.json
```

Global configuration:

```text
$XDG_CONFIG_HOME/metis-agent/config.json
```

Or the platform-equivalent configuration directory.

Copy the example file to get started:

```bash
cp .metis-agent.example.json .metis-agent.json
```

The project config can contain API keys and MCP authentication tokens. It is git-ignored by default. Commit the example file, never real credentials.

### Notable fields

| Field | Default | Meaning |
|---|---|---|
| `redact_secrets` | `true` | Mask credentials in tool output |
| `verify` | `""` | Command to run after a turn changes files |
| `verify_timeout_sec` | `120` | Verification timeout |
| `instructions` | `[]` | Extra instruction files |
| `fallback_models` | `[]` | Models to try when the active model fails |
| `thinking` | `""` | Reasoning effort: `none`, `low`, `medium`, `high`, `xhigh` |
| `embedding_model` | `""` | Embedding model for semantic search |

MCP servers can be configured in the `mcp_servers` array or through `METIS_MCP_SERVERS`.

### Environment variables

Environment variables use the `METIS_` prefix.

Examples:

```text
METIS_PROVIDER
METIS_MODEL
METIS_AUTO_APPROVE
METIS_SHELL_TIMEOUT_SEC
METIS_REDACT_SECRETS
METIS_VERIFY
METIS_VERIFY_TIMEOUT_SEC
METIS_INSTRUCTIONS
METIS_FALLBACK_MODELS
METIS_THINKING
METIS_EMBEDDING_MODEL
```

See `internal/config/config.go` for the complete configuration set.

### Instruction files

Metis can load additional instruction files into its system prompt.

Global files:

```text
AGENTS.md
METIS.md
rules.md
```

Project files:

```text
AGENTS.md
METIS.md
CLAUDE.md
.metis-agent/rules.md
.metis-agent/instructions.md
```

Additional paths can be supplied through the `instructions` setting or `--instructions` flag.

Loaded files are appended to the system prompt with their path as a heading.

---

## CLI reference

| Flag | Default | Description |
|---|---|---|
| `--provider` | `auto` | Provider: `auto`, `llama-prism`, `lmstudio`, `ollama`, `openai`, `openrouter`, `groq`, `together`, `compatible` |
| `--base-url` | preset | OpenAI-compatible base URL |
| `--api-key` | env | API key; provider environment variables are preferred |
| `--model` | auto | Model name |
| `--port` | — | Legacy shortcut for `http://127.0.0.1:PORT` |
| `--temp` | `0.3` | Temperature |
| `--max-steps` | `40` | Maximum model/tool iterations per task |
| `--window` | `64` | Conversation messages kept in context |
| `--system` | Metis prompt | Override the system prompt |
| `--dir` | `.` | Working directory for tools and shell |
| `--no-shell` | `false` | Disable the shell tool |
| `--shell-timeout` | `120` | Shell timeout in seconds; maximum 900 |
| `--auto-approve`, `-y`, `--yes` | `false` | Run dangerous tool calls without asking |
| `--approve-edits` | `false` | Review in-project file edits with a diff |
| `--redact-secrets`, `--no-redact` | on | Redact credentials in tool output |
| `--verify CMD` | — | Run a command after a turn that changed files |
| `--verify-timeout N` | `120` | Automatic verification timeout |
| `--fallback-models L` | — | Comma-separated fallback models |
| `--instructions L` | — | Comma-separated instruction files |
| `--embedding-model M` | — | Embedding model for semantic search |
| `--highlight` | `auto` | Syntax palette: `auto`, `dark`, `light`, `off` |
| `--config` | — | Load a specific config file |
| `--no-config` | `false` | Ignore config files |
| `--init-config` | `false` | Write a starter config file and exit |
| `--list-models` | `false` | List models from reachable providers |
| `--session NAME` | — | Load or create a named session |
| `--resume` | `false` | Resume the latest session for `--dir` |
| `--no-session` | `false` | Disable session persistence |
| `--plain` | `false` | Use the line-based REPL |
| `--json` | `false` | Stream NDJSON events; implies non-TUI |
| `--acp` | `false` | Run as an Agent Client Protocol server |
| `--once TASK` | — | Run a single task without the TUI |
| `--doctor` | `false` | Diagnose installation and model backend |
| `--update` | `false` | Download and install the latest release |
| `--check-update` | `false` | Check whether a newer release exists |
| `--undo` | `false` | Restore files changed by the previous turn |
| `--version` | — | Print version and exit |
| `-h`, `--help` | — | Show help |

---

## Examples

Start an interactive session:

```bash
./metis-agent
```

Use the plain REPL:

```bash
./metis-agent --plain
```

Run a one-shot task:

```bash
./metis-agent --once "What time is it?"
```

Run tests and ask Metis to fix failures:

```bash
./metis-agent --auto-approve --once "Run the tests and fix them."
```

Work in a specific directory:

```bash
./metis-agent --dir /tmp/code --once "Fix the failing tests and rerun them."
```

Explore without shell access:

```bash
./metis-agent --dir /tmp/code --no-shell --once "Summarize the repo."
```

Create and verify a file:

```bash
./metis-agent --once "Write 'hello metis' to note.txt, then read it back."
```

Enable automatic verification:

```bash
./metis-agent --verify "go test ./..." --once "Fix the failing tests."
```

Review edits before they are written:

```bash
./metis-agent --approve-edits --once "Rename the helper and update callers."
```

Stream JSON events:

```bash
./metis-agent --json --once "Summarize the repository."
```

Run diagnostics:

```bash
./metis-agent --doctor
```

---

## Project structure

```text
metis-agent/
├── go.mod
├── main.go
├── features.go
├── agent_build.go
├── scripts/
│   ├── install.sh
│   └── install.ps1
└── internal/
    ├── tui/
    │   ├── tui.go
    │   └── styles.go
    ├── llm/
    │   ├── client.go
    │   └── router.go
    ├── agent/
    │   └── agent.go
    ├── memory/
    │   └── history.go
    ├── tools/
    │   ├── registry.go
    │   ├── approval.go
    │   ├── approval_preview.go
    │   ├── tool.go
    │   ├── file.go
    │   ├── fs.go
    │   ├── project.go
    │   ├── repomap.go
    │   ├── semantic.go
    │   ├── diagnostics.go
    │   ├── websearch.go
    │   ├── subagent.go
    │   ├── git.go
    │   ├── json.go
    │   ├── system.go
    │   ├── todo.go
    │   ├── web.go
    │   └── shell.go
    ├── config/
    ├── checkpoint/
    ├── embeddings/
    ├── acp/
    ├── instructions/
    ├── redact/
    ├── update/
    ├── doctor/
    └── session/
```

### Key files

| File | Responsibility |
|---|---|
| `main.go` | CLI flags and TUI/REPL/one-shot entrypoints |
| `features.go` | JSON events, undo, commit, verification, and subagent wiring |
| `agent_build.go` | Shared agent construction for CLI and ACP sessions |
| `internal/tui/tui.go` | Bubble Tea model, input, events, and layout |
| `internal/tui/styles.go` | UI palette and transcript rendering |
| `internal/llm/client.go` | OpenAI-compatible client and SSE streaming |
| `internal/llm/router.go` | Runtime model switching and fallback chain |
| `internal/agent/agent.go` | Agent loop and progress events |
| `internal/memory/history.go` | Conversation history and validated sliding window |
| `internal/tools/registry.go` | Tool registry and orchestration |
| `internal/tools/approval.go` | Dangerous-action classification and approval state |
| `internal/tools/approval_preview.go` | Diff previews for edit approvals |
| `internal/config/config.go` | Configuration, environment variables, and platform paths |
| `internal/session/session.go` | Per-project session persistence |

Other tool files implement filesystem operations, repository maps, semantic search, diagnostics, Git, web access, shell execution, and system information.

---

## How the agent loop works

Metis follows a tool-driven agent loop:

1. The agent sends the system prompt, recent conversation history, and tool schemas to the model server with `stream: true`.
2. Text deltas are forwarded to the UI as they arrive.
3. If the model requests tools, Metis executes them in registration order using the run context. Cancellation can stop long-running operations.
4. Tool results are appended to the conversation, and the model is called again.
5. When the model responds without tool calls, the turn finishes.
6. If files changed and automatic verification is configured, Metis runs the verification command and can send failures back to the model for repair attempts.

The history window trims older messages without leaving orphaned tool results or incomplete tool-call groups.

`/compact` can replace a long conversation with a model-generated summary.

The LLM client also accepts servers that ignore `stream: true` and return a single JSON completion.

---

## Development

Run the project checks:

```bash
make check   # gofmt + vet + build + tests
make race    # tests with the race detector
make cover   # coverage summary
```

CI runs:

- `gofmt`
- `go vet`
- `go build`
- `go test -race`
- Cross-builds for macOS and Windows

Contributions should preserve existing behavior unless a change explicitly intends to alter it. When changing architecture, tools, configuration, or user-facing behavior, update the relevant documentation as part of the same change.

---

## License

Metis Agent is released under the **MIT License**. See [LICENSE](https://github.com/4matthedev/metis-agent/blob/main/LICENSE).

---

<p align="center">
  <strong>Metis Agent</strong><br>
  Intelligence is not just knowing. It's knowing how to act.
</p>
