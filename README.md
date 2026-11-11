 <div align="center">

```text
 ███╗   ███╗███████╗████████╗██╗███████╗    █████╗  ██████╗ ███████╗███╗   ██╗████████╗
 ████╗ ████║██╔════╝╚══██╔══╝██║██╔════╝   ██╔══██╗██╔════╝ ██╔════╝████╗  ██║╚══██╔══╝
██╔████╔██║█████╗     ██║   ██║███████╗   ███████║██║  ███╗█████╗  ██╔██╗ ██║   ██║
██║╚██╔╝██║██╔══╝     ██║   ██║╚════██║   ██╔══██║██║   ██║██╔══╝  ██║╚██╗██║   ██║
██║ ╚═╝ ██║███████╗   ██║   ██║███████║   ██║  ██║╚██████╔╝███████╗██║ ╚████║   ██║
╚═╝     ╚═╝╚══════╝   ╚═╝   ╚═╝╚══════╝   ╚═╝  ╚═╝ ╚═════╝ ╚══════╝╚═╝  ╚═══╝   ╚═╝
```

### YOUR CODEBASE. YOUR MODELS. YOUR MACHINE.

**An autonomous coding agent built for developers who want control.**

Metis explores your codebase, plans changes, edits files, runs tools, and verifies results — powered by the models and infrastructure you choose.

<p>
  <a href="https://github.com/4matthedev/metis-agent/stargazers"><img src="https://img.shields.io/github/stars/4matthedev/metis-agent?style=for-the-badge&logo=github&color=181717" alt="GitHub stars"></a>
  <a href="https://github.com/4matthedev/metis-agent/blob/main/LICENSE"><img src="https://img.shields.io/github/license/4matthedev/metis-agent?style=for-the-badge&color=blue" alt="License"></a>
  <img src="https://img.shields.io/badge/Go-1.25%2B-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go 1.25+">
  <img src="https://img.shields.io/badge/Local--First-AI-7C3AED?style=for-the-badge" alt="Local First">
</p>

[Getting Started](#-getting-started) · [Features](#-what-makes-metis-different) · [Documentation](#-documentation) · [Configuration](#-configuration) · [Contributing](#-development)

</div>

---

## The idea

Most coding agents give you a model wrapped in a tool interface. Metis is built around something more practical: **an agent that can understand a project, take action, and check its own work.**

Give it a task. Metis explores the repository, identifies the relevant files, chooses the appropriate tools, makes changes, and can run your tests to verify the result.

You choose the model. You choose the provider. You choose how much autonomy to allow.

```text
you    fix the failing test in calc.py

metis  Let me inspect the project first.
       list_dir   .  (depth 2)
       ok         calc.py

       shell      $ python -m unittest -v
       failed     test_add ... FAIL
                  AssertionError: -1 != 5

       file_edit  calc.py
       ok         edited calc.py

       shell      $ python -m unittest -v
       ok         Ran 2 tests in 0.000s

metis  Fixed `add` and both tests pass.

 ● ready                                             5 steps · 1.2s
```

## ✦ What makes Metis different

| | Capability | What it means |
|---|---|---|
| ◈ | **Local-first** | Run local models through Ollama, llama.cpp, LM Studio, or llama-prism. |
| ⌘ | **Model freedom** | Use OpenAI, OpenRouter, Groq, Together, or any compatible endpoint. |
| ⚙ | **Real tool use** | Explore files, edit code, inspect Git, execute commands, and run diagnostics. |
| ◉ | **Multiple operating modes** | Switch between General, Planner, Builder, Debugger, Cybersecurity, and Simplifier. |
| ✓ | **Verification loop** | Run tests after changes and let Metis respond to failures. |
| ⛨ | **Approval controls** | Review risky operations and inspect file-edit diffs before writing. |
| ↺ | **Undo and recovery** | Restore files changed during the previous turn. |
| ⇄ | **Extensible** | Connect MCP servers and load reusable Skills. |

### Built around your workflow

- **Terminal-native:** Full-screen TUI, plain REPL, and one-shot execution.
- **Inspectable:** Tool calls and results are visible as the agent works.
- **Configurable:** Control providers, models, context, permissions, and verification.
- **Composable:** JSON event streaming and Agent Client Protocol support.

---

## ◈ Getting started

### One-line installation

**Linux / macOS — Easy mode**

Installs Ollama if needed, pulls `gemma4:e2b`, installs Metis, and configures the initial setup.

```bash
curl -fsSL https://raw.githubusercontent.com/4matthedev/metis-agent/main/scripts/install.sh | bash
```

**Linux / macOS — Advanced mode**

Choose your backend: Ollama, llama.cpp, or LM Studio. The installer reuses an available model or selects a Gemma variant based on detected RAM.

```bash
curl -fsSL https://raw.githubusercontent.com/4matthedev/metis-agent/main/scripts/install.sh | bash -s -- --advanced
```

**Windows — PowerShell**

Windows 10 1803+ is required for the bundled `tar.exe`.

```powershell
irm https://raw.githubusercontent.com/4matthedev/metis-agent/main/scripts/install.ps1 | iex
```

The Windows installer can install Ollama through `winget`, pull `gemma4:e2b`, verify the release checksum, install Metis under `%LOCALAPPDATA%\metis-agent\bin`, and add that directory to the user `PATH`.

Supported Windows installer options:

`-Backend none` · `-Model NAME` · `-NoModel` · `-InstallDir DIR` · `-Version TAG` · `-DryRun` · `-Uninstall`

Run `install.ps1 -Help` for the full list. Native `windows/arm64` builds are not published yet.

### Installer options

| Platform | Useful options |
|---|---|
| Linux / macOS | `--backend ollama\|llamacpp\|lmstudio\|none` |
| Linux / macOS | `--model NAME`, `--no-model`, `--prefix DIR` |
| Linux / macOS | `--version TAG`, `--yes`, `--dry-run` |
| Windows | `-Backend`, `-Model`, `-NoModel`, `-InstallDir` |
| Windows | `-Version`, `-DryRun`, `-Uninstall` |

Run `scripts/install.sh --help` or `install.ps1 -Help` for details.

### Where Metis lives

| Location | Purpose |
|---|---|
| `~/.local/share/metis-agent/bin/metis-agent` | Binary on Linux |
| `~/Library/Application Support/metis-agent` | Application data on macOS |
| `%LOCALAPPDATA%\metis-agent\bin` | Binary on Windows |
| `~/.local/bin/metis-agent` | Launcher |
| `~/.config/metis-agent/config.json` | Linux configuration |
| `~/Library/Application Support/metis-agent/config.json` | macOS configuration |
| `%APPDATA%\metis-agent\config.json` | Windows configuration |
| `~/.local/share/metis-agent/backend.log` | Backend logs |

On first run, the installer adds `~/.local/bin` to your shell rc file. Re-running the installer upgrades the binary and backs up an existing config first.

### Uninstall

Use the installer to uninstall rather than deleting files manually.

```bash
curl -fsSL https://raw.githubusercontent.com/4matthedev/metis-agent/main/scripts/install.sh | bash -s -- --uninstall
```

```powershell
.\install.ps1 -Uninstall
```

Uninstall removes the binary, launcher, and PATH entry while keeping configuration, models, sessions, and undo history. Add `--purge` (or `-Purge` on Windows) to remove those too. Ollama, llama.cpp, and LM Studio are left installed.

---

## Requirements

- Go 1.25+ only if building from source.
- Linux, macOS, or Windows 10 1803+.
- A running OpenAI-compatible model server with streaming tool/function calling.
- At least one model loaded on local servers, or pass `--model`.

Supported local endpoints include:

| Backend | Typical endpoint |
|---|---|
| llama-prism | `http://127.0.0.1:9931` |
| LM Studio | `http://127.0.0.1:1234` |
| Ollama | `http://127.0.0.1:11434` |

Hosted providers include OpenAI, OpenRouter, Groq, Together, and compatible endpoints.

Use `--list-models` to discover reachable providers and their models.

## Build from source

```bash
git clone https://github.com/4matthedev/metis-agent.git
cd metis-agent
go build -o metis-agent .
```

The TUI is built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Bubbles](https://github.com/charmbracelet/bubbles), and [Lip Gloss](https://github.com/charmbracelet/lipgloss).

Go fetches dependencies on the first build. The result is a self-contained binary.

## Run Metis

```bash
./metis-agent                              # Full-screen TUI
./metis-agent --plain                      # Line-based REPL
./metis-agent --once "What time is it?"    # One-shot execution
```

Metis automatically uses the TUI when stdin and stdout are terminals. Piped input or `--plain` uses the REPL, which is useful for scripts.

---

## ⌘ The terminal experience

### TUI shortcuts

| Key | Action |
|---|---|
| `Enter` | Send the current message |
| `Alt+Enter`, `Ctrl+J` | Insert a newline |
| `Ctrl+P` / `Ctrl+N` | Previous / next input-history message |
| `PgUp` / `PgDn` | Scroll the transcript |
| `Ctrl+O` | Expand / collapse tool output |
| `Esc`, `Ctrl+C` | Cancel the running task |
| `Ctrl+L` | Clear the transcript |
| `Ctrl+R` | Clear transcript and conversation |
| `Ctrl+C` (idle), `Ctrl+D` | Quit |
| `y` / `a` / `A` / `n` | Answer an approval prompt |
| `↑` / `↓` | Navigate the slash-command menu |
| `Tab`, `Enter` | Complete the selected command |

When the input is empty, `Tab` also cycles the operating mode.

### Slash commands

Type `/` to open the command menu. It filters as you type. Use `↑` / `↓` to select, then `Tab` or `Enter` to complete. Press `Esc` to dismiss it.

| Command | Action |
|---|---|
| `/help` | Show commands and keys |
| `/clear` | Clear the transcript |
| `/reset` | Clear transcript and conversation memory |
| `/status` | Show provider, model, project, and approval mode |
| `/tools` | List available tools |
| `/expand` | Toggle full tool output |
| `/session` | Show the current session and its file |
| `/sessions` | List recent sessions for this directory |
| `/new` | Start a fresh session |
| `/resume [id]` | Resume the latest or a named session |
| `/autoapprove [on\|off]` | Toggle automatic approval |
| `/approve-edits [on\|off]` | Review in-project edits with a diff |
| `/model [name]` | List or switch models |
| `/thinking [level]` | Set reasoning effort |
| `/theme [auto\|dark\|light]` | Switch syntax-highlighting palette |
| `/compact` | Summarize the conversation and free context |
| `/context` | Show message and approximate token usage |
| `/undo` | Restore files changed by the previous turn |
| `/commit [message]` | Stage everything and commit |
| `/copy` | Copy the last assistant message |
| `/mcp [subcommand]` | Manage MCP servers |
| `/skills [subcommand]` | Manage Skills |
| `/mode [name]` | Switch operating mode |
| `/quit` | Leave Metis |

### MCP commands

- `/mcp` or `/mcp list` — list configured MCP servers and their tools.
- `/mcp add {json}` — add a server.
- `/mcp remove <name>` — disconnect and remove a server.
- `/mcp reconnect <name>` — reconnect a server.

Servers are persisted to `./.metis-agent.json` and reconnected on startup.

### Skills commands

- `/skills` or `/skills list` — list available Skills.
- `/skills info <name>` — show a Skill's full body.
- `/skills use <name>` — load its body into context for the rest of the task.
- `/skills run <name>` — execute its declared `run` command.

A Skill is a `SKILL.md` file with a `name`, `description`, an optional `run` command, and a Markdown body. Running a Skill is approval-gated, just like `shell`.

The plain REPL supports `/help`, `/status`, `/tools`, `/reset`, `/autoapprove`, `/approve-edits`, `/model`, `/compact`, `/context`, `/undo`, `/commit`, `/mcp`, and `/skills`. Entering `/` alone prints the command list.

Each prompt keeps previous turns in the conversation, bounded by `--window`. Use `/reset` to start fresh.

---

## ◉ Operating modes

Metis has six operating modes. Each mode adds a specialized section to the system prompt while preserving the shared tool list and core rules.

| Mode | Focus |
|---|---|
| **General** | Explore, plan, build, and verify end-to-end. |
| **Planner** | Analyze the codebase and propose an approach without modifying code. |
| **Builder** | Implement and refine code with precise edits and verification. |
| **Debugger** | Reproduce failures, isolate root causes, and make focused fixes. |
| **Cybersecurity** | Audit code for vulnerabilities and report severity and fixes. |
| **Simplifier** | Reduce complexity without changing behavior. |

Switch modes with `Tab` when the input is empty, or use `/mode <name>`:

`general` · `planner` · `builder` · `debugger` · `cybersecurity` · `simplifier`

The active mode appears in the status bar and is confirmed in the transcript.

**Planner mode is enforced at the tool-registry level.** Mutating tools are hidden and refused, including `shell`, file-writing tools, `download_file`, non-GET `http_fetch`, external MCP tools, and Skills `run` — even when `--auto-approve` is enabled.

Implementation: `internal/agent/modes.go`.

---

## ⛨ Approvals and safety

Metis does not confine tools to a filesystem sandbox. Relative paths resolve against `--dir`; absolute paths, `~`, `..`, and symlinks are allowed.

To make that power more deliberate, the registry requests approval before operations that may be destructive or access locations outside the project.

Approval is required for:

- Every `shell` command.
- `file_delete` and other deletion operations.
- `download_file`.
- Non-`GET` / `HEAD` `http_fetch`.
- Non-dry-run `replace_in_files`.
- `env_get` with `show_secrets=true`.
- `diagnostics`, which runs build and lint programs.
- Reads or writes outside the project directory.

### Review edits before writing

Run with `--approve-edits` or `/approve-edits on` to review in-project `file_write`, `file_edit`, and `multi_edit` operations.

Metis computes a unified diff without touching disk. The approval prompt shows the exact proposed change before it is written.

### Credential redaction

Tool output is passed through a credential redactor by default. It masks:

- PEM private keys.
- Bearer tokens.
- `api_key`, `password`, and `secret` assignments.
- Common token shapes such as `sk-…`, `ghp_…`, `AKIA…`, `xox…`, and `glpat-…`.

Disable redaction with `--no-redact`, `METIS_REDACT_SECRETS=false`, or `"redact_secrets": false` when debugging a false positive.

### Approval controls

| Key | Decision |
|---|---|
| `y` | Allow this call once |
| `a` | Always allow this tool for the session |
| `A` | Auto-approve every dangerous call for the session |
| `n`, `Esc` | Deny the call and stop the step |

The plain REPL uses the same choices. Denied calls are reported to the model as errors, and the model is told not to retry them.

Dangerous calls can be allowed up front with `--auto-approve` (`-y` or `--yes`), `METIS_AUTO_APPROVE=true`, or `"auto_approve": true` in a config file. You can also toggle it with `/autoapprove`.

In non-interactive runs, dangerous tools are denied unless auto-approval is enabled.

---

## ↺ Undo, verification, and context

### Undo

Before a mutating tool changes a path, Metis snapshots its previous state.

Each user turn creates one checkpoint. `/undo` or `metis-agent --undo` restores the most recent checkpoint:

- Newly created files are removed.
- Deleted files and directories are restored.
- The last 30 checkpoints per project are retained.

Checkpoints live under the platform data directory:

`.../metis-agent/checkpoints/<project-hash>`

Very large files and turns exceeding the snapshot budget are reported as skipped.

### Automatic verification

Set `verify` or `--verify` to a command that checks whether a change works.

Examples:

```bash
go test ./...
npm test
```

Verification runs only after a turn that actually changed files. If it fails, the failure is sent back to the model as a new instruction. Metis can make up to two repair attempts before showing the final failure.

Use `--verify-timeout` to bound each run.

### Context management

- `/context` shows message count and approximate token usage against `--window`.
- `/compact` asks the model to summarize the session and replaces the history with that summary.

This lets longer sessions continue while retaining important decisions, files, and next steps.

---

## ⇄ Integrations

### MCP servers

Metis supports the [Model Context Protocol](https://modelcontextprotocol.io/). MCP server tools are exposed as regular Metis tools and namespaced as `<server>__<tool>` to avoid collisions.

Every MCP tool call requires the same approval prompt as a `shell` call. It is auto-approved only when `auto_approve` is enabled or `--yes` is passed.

A failed connection is logged as a warning; Metis continues with the remaining tools.

Configure MCP servers in the `mcp_servers` array or through `METIS_MCP_SERVERS`.

Example:

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

`type` can be `stdio` (default, requires `command`) or `http` (requires `url`, with `streamable` or `sse` transport). If omitted, `name` is derived from the command path or URL host.

### Providers and models

`--provider auto` probes llama-prism, LM Studio, and Ollama.

If a local provider has exactly one loaded model, Metis selects it. Otherwise, pass `--model`.

Hosted providers use their usual API key environment variables:

- `OPENAI_API_KEY`
- `OPENROUTER_API_KEY`
- `GROQ_API_KEY`
- `TOGETHER_API_KEY`

You can also pass `--api-key`.

```bash
./metis-agent --list-models

./metis-agent --provider ollama --model MODEL
./metis-agent --provider lmstudio --model MODEL
./metis-agent --provider openai --model MODEL

./metis-agent --base-url http://127.0.0.1:9931 --model MODEL
./metis-agent --provider compatible --base-url https://example.com/v1 --model MODEL
```

If a local server reports an unknown or unloaded model, Metis displays a clear `MODEL NOT LOADED` message instead of a raw HTTP dump.

### Semantic search

Configure an embedding model to enable the `semantic_search` tool.

```bash
ollama pull nomic-embed-text
metis-agent --embedding-model nomic-embed-text
```

Metis chunks the repository, creates embeddings through the provider's OpenAI-compatible `/v1/embeddings` endpoint, and caches the index under the data directory.

The cache is keyed by model and invalidated when file sizes or timestamps change. Pass `rebuild=true` to the tool after large edits.

Without an embedding model, `semantic_search` is not offered to the model. Any provider exposing `/v1/embeddings` can be used.

---

## ⚡ JSON events and editor integration

### JSON event stream

Use `--json` to stream newline-delimited JSON objects to stdout:

`step` · `text` · `tool_call` · `tool_result` · `done` · `error`

Human-facing notices stay on stderr.

Combine it with `--once` for a request/response adapter, or with `--plain` for an interactive stream.

### Agent Client Protocol

Run `--acp` to expose Metis as an [Agent Client Protocol](https://agentclientprotocol.com) server over stdin/stdout.

Implemented methods:

- `initialize`
- `session/new`
- `session/prompt`
- `session/cancel`
- `session/set_mode`
- `session/close`

Metis streams `session/update` notifications for text and tool calls. Tool approvals use `session/request_permission`, including diff previews from `--approve-edits`.

Sessions are independent agents with their own working directory.

Not yet implemented: `session/load`, client `fs/*` and `terminal/*` methods, and per-session MCP servers. Metis uses its configured MCP servers.

---

## ⚙ Configuration

Settings can come from:

1. Global configuration.
2. Project configuration.
3. Environment variables.
4. Command-line flags.

Later sources override earlier ones.

Run `--init-config` to write a starter file.

Environment variables use the `METIS_` prefix. Examples:

`METIS_PROVIDER` · `METIS_MODEL` · `METIS_AUTO_APPROVE` · `METIS_SHELL_TIMEOUT_SEC` · `METIS_REDACT_SECRETS` · `METIS_VERIFY` · `METIS_VERIFY_TIMEOUT_SEC` · `METIS_INSTRUCTIONS` · `METIS_FALLBACK_MODELS` · `METIS_THINKING` · `METIS_EMBEDDING_MODEL`

See `internal/config/config.go` for the complete list.

### Notable configuration fields

| Field | Default | Meaning |
|---|---|---|
| `redact_secrets` | `true` | Mask credentials in tool output |
| `verify` | `""` | Command to run after a turn changes files |
| `verify_timeout_sec` | `120` | Verification timeout |
| `instructions` | `[]` | Extra instruction files loaded into the system prompt |
| `fallback_models` | `[]` | Models to try when the active model fails |
| `thinking` | `""` | Reasoning effort: `none`, `low`, `medium`, `high`, `xhigh` |
| `embedding_model` | `""` | Embedding model for `semantic_search` |

### Instruction files

Metis loads instruction files from:

- Global config directory: `AGENTS.md`, `METIS.md`, `rules.md`.
- Project directory: `AGENTS.md`, `METIS.md`, `CLAUDE.md`, `.metis-agent/rules.md`, `.metis-agent/instructions.md`.
- Any additional paths listed in `instructions`.

They are appended to the system prompt with their path as a heading.

### Project configuration

The project config is `./.metis-agent.json`.

The global config is stored under `$XDG_CONFIG_HOME/metis-agent/config.json`, or the platform equivalent.

Start with the example:

```bash
cp .metis-agent.example.json .metis-agent.json
```

Configuration can contain `api_key` and MCP `auth_token` values, so `.metis-agent.json` is git-ignored by default. Commit the example file, never real credentials.

MCP servers can be configured in `mcp_servers` or through `METIS_MCP_SERVERS`.

---

## 🧰 Built-in tools

Metis exposes tools to the model through a shared registry.

| Tool | Purpose |
|---|---|
| `time` | Current local date/time in ISO 8601 |
| `list_dir` | Explore directory trees |
| `glob` | Find files by glob pattern |
| `project_info` | Detect languages, manifests, Git branch, file counts, and layout |
| `file_read` | Read a file or line range |
| `file_read_many` | Read multiple related files |
| `file_stat` | Inspect file type, size, mode, modification time, and line count |
| `search` | Regex-search file contents |
| `hash_file` | Compute or verify MD5, SHA-1, SHA-256, and SHA-512 digests |
| `json_query` | Extract paths from JSON |
| `file_edit` | Precisely replace text in an existing file |
| `multi_edit` | Apply several related replacements atomically |
| `replace_in_files` | Regex or literal replacement across files |
| `file_write` | Create or overwrite a file |
| `make_dir` | Create directory trees |
| `file_move` | Move or rename files and directories |
| `file_copy` | Copy files |
| `file_delete` | Delete files or directories |
| `diff_files` | Compare files with unified diff |
| `git_status` | Inspect working-tree status |
| `git_diff` | Inspect staged and unstaged changes |
| `git_log` | Read recent commits |
| `git_show` | Inspect commits, tags, or objects |
| `git_blame` | Inspect line-by-line authorship |
| `http_fetch` | Make HTTP(S) requests and read text, JSON, or HTML |
| `web_search` | Search the public web without an API key |
| `download_file` | Download a URL atomically into a file |
| `repo_map` | Build a compact symbol map across languages |
| `semantic_search` | Search by meaning using a cached embedding index |
| `diagnostics` | Run detected build, type-check, and lint commands |
| `subagent` | Delegate independent read-only investigation |
| `system_info` | Inspect OS, architecture, CPU count, user, temp path, and time |
| `env_get` | Read environment variables with secret redaction |
| `which` | Locate executables on PATH |
| `port_check` | Check whether TCP ports accept connections |
| `todo_write` | Replace the task list for multi-step work |
| `todo_read` | Read the current task list |
| `shell` | Run non-interactive `bash -c` commands |

### How Metis uses its tools

Metis is instructed to:

1. Explore the project before making changes.
2. Plan multi-step work with `todo_write`.
3. Use `repo_map` to orient itself in large codebases.
4. Prefer `file_edit` or `multi_edit` for precise changes.
5. Prefer filesystem and Git tools over shell where possible.
6. Verify changes with `diagnostics` or builds and tests through `shell`.
7. Use `replace_in_files` with `dry_run=true` before broad mechanical refactors.

`subagent` launches a separate read-only agent with its own context, keeping broad research out of the main conversation.

**Path access:** Relative paths resolve against `--dir`. Absolute paths, `~`, `..`, and symlinks are allowed. Operations outside the project are not blocked, but require approval first.

**Shell:** Commands start in `--dir` and can access anything your user account can access, including the network. Every shell call requires approval unless auto-approval is enabled. Use `--no-shell` when you want filesystem tools only.

Shell output is ANSI-cleaned and capped (head + tail). Long commands can be cancelled with `Esc` / `Ctrl+C`.

---

## 🧪 Examples

```bash
# Launch the TUI
./metis-agent

# Use the plain REPL
./metis-agent --plain

# Ask a one-shot question
./metis-agent --once "What time is it?"

# Run tests and let Metis fix failures
./metis-agent --auto-approve --once "Run the tests and fix them."

# Work in a specific directory
./metis-agent --dir /tmp/code --once "Fix the failing tests and rerun them."

# Disable shell access
./metis-agent --dir /tmp/code --no-shell --once "Summarize the repo."

# Create and verify a file
./metis-agent --once "Write 'hello metis' to note.txt, then read it back."

# Limit the number of steps
./metis-agent --max-steps 3 --once "Do something complicated"

# Verify changes automatically
./metis-agent --verify "go test ./..." --once "Fix the failing tests."

# Review edits before writing
./metis-agent --approve-edits --once "Rename the helper and update callers."

# Stream JSON events
./metis-agent --json --once "Summarize the repository."

# Diagnose the installation
./metis-agent --doctor

# Show all options
./metis-agent --help
```

---

## 🏗 Project layout

```text
metis-agent/
├── go.mod
├── main.go                         # CLI flags, TUI/REPL/one-shot entrypoints
├── features.go                     # JSON events, undo/commit/verify, subagent wiring
├── agent_build.go                  # Shared agent construction for CLI and ACP
├── scripts/
│   ├── install.sh                  # Linux/macOS installer
│   └── install.ps1                 # Windows installer
└── internal/
    ├── tui/                        # Bubble Tea model, input, events, layout
    ├── llm/                        # OpenAI-compatible client and model router
    ├── agent/                      # Agent loop and progress events
    ├── memory/                     # History and validated sliding window
    ├── tools/                      # Tool registry and built-in tools
    ├── config/                     # Config files, environment, platform paths
    ├── checkpoint/                 # Per-turn snapshots for /undo
    ├── embeddings/                 # Cached embedding index
    ├── acp/                        # Agent Client Protocol server
    ├── instructions/               # AGENTS.md / METIS.md / rules loading
    ├── redact/                     # Credential redaction
    ├── update/                     # Release checks and verified updates
    ├── doctor/                     # Installation diagnostics
    └── session/                    # Per-project session persistence
```

---

## 🧠 Under the hood: the agent loop

Metis follows a tool-driven loop:

1. The agent sends the system prompt, recent history, and tool schemas to the model server with `stream: true`.
2. Text deltas are forwarded to the UI immediately.
3. If the model requests tools, Metis executes them in registration order using the run context. Cancellation can stop a long-running command.
4. Tool results are appended as `tool` messages, and the model is called again.
5. When the model responds without tool calls, the turn finishes.
6. If files changed and verification is configured, Metis runs the verification command and feeds failures back to the model for up to two repair attempts.

A validated sliding window trims old messages without leaving orphaned tool results or incomplete tool-call groups in the prompt.

When a session gets long, `/compact` can replace the history with a model-written summary.

The client also accepts servers that ignore `stream: true` and return a single JSON completion.

---

## 🛠 Development

Run the standard checks:

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

---

## 🚀 CLI reference

| Flag | Default | Description |
|---|---|---|
| `--provider` | `auto` | `auto`, `llama-prism`, `lmstudio`, `ollama`, `openai`, `openrouter`, `groq`, `together`, `compatible` |
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
| `--shell-timeout` | `120` | Default shell timeout in seconds; maximum 900 |
| `--auto-approve`, `-y`, `--yes` | `false` | Run dangerous tool calls without asking |
| `--approve-edits` | `false` | Review in-project edits before writing |
| `--redact-secrets`, `--no-redact` | on | Redact credentials in tool output |
| `--verify CMD` | — | Run CMD after a turn that changed files |
| `--verify-timeout N` | `120` | Automatic verification timeout |
| `--fallback-models L` | — | Comma-separated fallback models |
| `--instructions L` | — | Comma-separated extra instruction files |
| `--embedding-model M` | — | Enable `semantic_search` |
| `--highlight` | `auto` | `auto`, `dark`, `light`, or `off` |
| `--config` | — | Load a specific config file |
| `--no-config` | `false` | Ignore config files |
| `--init-config` | `false` | Write a starter config file and exit |
| `--list-models` | `false` | List models from reachable providers |
| `--session NAME` | — | Load or create a named session |
| `--resume` | `false` | Resume the latest session for `--dir` |
| `--no-session` | `false` | Disable session persistence |
| `--plain` | `false` | Use the line REPL instead of the TUI |
| `--json` | `false` | Stream NDJSON events; implies non-TUI |
| `--acp` | `false` | Run as an Agent Client Protocol server |
| `--once TASK` | — | Single-shot mode |
| `--doctor` | `false` | Diagnose installation and model backend |
| `--update` | `false` | Download and install the latest release |
| `--check-update` | `false` | Check whether a newer release exists |
| `--undo` | `false` | Restore files changed by the previous turn |
| `--version` | — | Print version and exit |
| `-h`, `--help` | — | Show help |

---

## 📄 License

Metis is released under the **MIT License**.

See [`LICENSE`](https://github.com/4matthedev/metis-agent/blob/main/LICENSE).

---

<div align="center">

**METIS — Practical intelligence for your codebase.**

Built for developers. Powered by your choice of models.

[GitHub Repository](https://github.com/4matthedev/metis-agent)

</div>
