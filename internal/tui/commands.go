package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"metis-agent/internal/llm"
)

// slashCommand describes one command shown in the completion menu.
type slashCommand struct {
	name string
	args string
	desc string
}

// maxSuggestions is a generous cap on completion rows; the real limit is the
// terminal height, so the whole command list fits in a typical terminal.
const maxSuggestions = 24

// slashCommands is the single source of truth for the completion menu. Aliases
// are accepted by command() but omitted here so the list stays short.
var slashCommands = []slashCommand{
	{"/help", "", "show commands and keys"},
	{"/clear", "", "clear the transcript"},
	{"/reset", "", "clear the transcript and forget the conversation"},
	{"/status", "", "show provider, model, project, and approval mode"},
	{"/tools", "", "list the available tools"},
	{"/expand", "", "toggle full tool output"},
	{"/session", "", "show the current session and its file"},
	{"/sessions", "", "list recent sessions for this directory"},
	{"/new", "", "start a fresh session"},
	{"/resume", "[id]", "resume the latest (or named) session"},
	{"/autoapprove", "[on|off]", "toggle or set automatic approval"},
	{"/approve-edits", "[on|off]", "review file edits with a diff before writing"},
	{"/model", "[name]", "list models or switch the active model"},
	{"/thinking", "[level]", "list thinking levels or set reasoning effort"},
	{"/theme", "[auto|dark|light]", "switch the color palette (auto = detect terminal)"},
	{"/compact", "", "summarize the conversation to free context"},
	{"/context", "", "show context usage"},
	{"/undo", "", "restore files changed by the previous turn"},
	{"/commit", "[message]", "stage everything and commit (generates a message)"},
	{"/mcp", "[list|add|remove|reconnect]", "manage MCP servers"},
	{"/mode", "[name]", "switch operating mode (general/planner/builder/debugger/cybersecurity/simplifier); press Tab to cycle"},
	{"/skills", "[list|info|use|run]", "manage the skill system"},
	{"/quit", "", "leave Metis"},
}

// parseModeName maps a mode name to its 0-based index in modes, or -1 if
// unrecognized.
func parseModeName(name string) int {
	switch name {
	case "general", "g", "":
		return 0
	case "planner", "p":
		return 1
	case "builder", "b":
		return 2
	case "debugger", "debug", "d":
		return 3
	case "cybersecurity", "cyber", "security", "sec", "c":
		return 4
	case "simplifier", "simple", "simplify", "simpl", "s":
		return 5
	default:
		return -1
	}
}

// currentModel returns the live model name, falling back to the initial value.
func (m *Model) currentModel() string {
	if m.opts.ModelName != nil {
		return m.opts.ModelName()
	}
	return m.opts.Model
}

func isExactCommand(text string) bool {
	for _, c := range slashCommands {
		if strings.EqualFold(text, c.name) {
			return true
		}
	}
	return false
}

// slashQuery returns the bare command prefix being typed. The menu appears for
// a single slash-prefixed word without arguments or newlines.
func (m *Model) slashQuery() (string, bool) {
	if m.running || m.pendingApproval != nil || m.suggestDismissed {
		return "", false
	}
	v := strings.TrimSpace(m.input.Value())
	if v == "" || !strings.HasPrefix(v, "/") {
		return "", false
	}
	if strings.ContainsAny(v, " \t\n") {
		return "", false
	}
	return strings.ToLower(v), true
}

// suggestions returns the commands matching the current input.
func (m *Model) suggestions() []slashCommand {
	q, ok := m.slashQuery()
	if !ok {
		return nil
	}
	out := make([]slashCommand, 0, len(slashCommands))
	for _, c := range slashCommands {
		if strings.HasPrefix(c.name, q) {
			out = append(out, c)
		}
	}
	return out
}

// suggestionLimit reports how many completion rows the terminal can show. When
// there is room, every command is listed; the cap leaves room for the header,
// status, input box, footer, and at least three transcript rows.
func (m *Model) suggestionLimit() int {
	n := len(m.suggestions())
	limit := maxSuggestions
	if n < limit {
		limit = n
	}
	if m.height > 0 {
		if avail := m.height - 10; avail < limit {
			limit = avail
		}
	}
	if limit < 0 {
		limit = 0
	}
	return limit
}

// paletteHeight reports how many rows the completion menu occupies.
func (m *Model) paletteHeight() int {
	n := len(m.suggestions())
	limit := m.suggestionLimit()
	if limit == 0 {
		return 0
	}
	if n > limit {
		return limit
	}
	return n
}

// completeSuggestion replaces the input with the selected command. Commands
// that take arguments leave a trailing space so the user can type them.
func (m *Model) completeSuggestion(list []slashCommand) {
	if len(list) == 0 {
		return
	}
	if m.suggestSel < 0 {
		m.suggestSel = 0
	}
	if m.suggestSel >= len(list) {
		m.suggestSel = len(list) - 1
	}
	value := list[m.suggestSel].name
	if list[m.suggestSel].args != "" {
		value += " "
	}
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.suggestDismissed = true
	m.suggestSel = 0
	m.layout()
	m.syncViewport(false)
}

// suggestionView renders the completion menu, scrolling a window around the
// selection when there are more commands than rows.
func (m *Model) suggestionView() string {
	list := m.suggestions()
	limit := m.suggestionLimit()
	if len(list) == 0 || limit == 0 {
		return ""
	}
	if m.suggestSel < 0 {
		m.suggestSel = 0
	}
	if m.suggestSel >= len(list) {
		m.suggestSel = len(list) - 1
	}
	start := 0
	if len(list) > limit {
		start = m.suggestSel - limit/2
		if start < 0 {
			start = 0
		}
		if start+limit > len(list) {
			start = len(list) - limit
		}
	}
	end := start + limit
	if end > len(list) {
		end = len(list)
	}
	width := max(20, m.width)
	var b strings.Builder
	for i := start; i < end; i++ {
		c := list[i]
		usage := c.name
		if c.args != "" {
			usage += " " + c.args
		}
		line := truncate(fmt.Sprintf("  %-24s %s", usage, c.desc), width)
		if i == m.suggestSel {
			b.WriteString(styleSuggest.Render(line))
		} else {
			b.WriteString(styleDim.Render(line))
		}
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (m *Model) submit() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	if strings.HasPrefix(text, "/") {
		return m.command(text)
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.running = true
	m.terminal = false
	m.runDone = false
	m.started = time.Now()
	m.elapsed = 0
	m.step = 0
	m.status = "thinking"
	m.err = ""
	m.activeAssistant = -1
	m.finalizeAssistant()

	m.blocks = append(m.blocks, block{kind: blockUser, text: text})
	m.history = append(m.history, text)
	m.histPos = len(m.history)

	m.input.Reset()
	m.input.SetHeight(1)
	m.input.Blur()
	m.layout()
	m.syncViewport(true)

	ag := m.agent
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		start := time.Now()
		answer, err := ag.Run(ctx, text)
		return runDoneMsg{answer: answer, err: err, elapsed: time.Since(start)}
	})
}

func (m *Model) command(text string) tea.Cmd {
	fields := strings.Fields(text)
	cmd := ""
	if len(fields) > 0 {
		cmd = strings.ToLower(fields[0])
	}
	switch cmd {
	case "/quit", "/exit", "/q":
		m.saveSession()
		return tea.Quit
	case "/clear", "/cls":
		m.blocks = nil
		m.addWelcome()
		m.input.Reset()
		m.status = "ready"
		m.layout()
		m.syncViewport(true)
	case "/reset":
		m.resetSessionHistory()
		m.blocks = nil
		m.addWelcome()
		m.input.Reset()
		m.err = ""
		m.status = "ready"
		m.elapsed = 0
		m.step = 0
		m.layout()
		m.syncViewport(true)
	case "/status":
		approval := "ask"
		if m.autoApprove {
			approval = "auto"
		}
		output := "collapsed"
		if m.expandTools {
			output = "expanded"
		}
		thinking := "none"
		if m.opts.ThinkingLevel != nil {
			thinking = m.opts.ThinkingLevel().String()
		}
		text := fmt.Sprintf("provider  %s\nmodel     %s\nthinking  %s\nserver    %s\nproject   %s\napproval  %s\ntools     %d\noutput    %s",
			m.opts.Provider, m.currentModel(), thinking, m.opts.Server, m.opts.Dir, approval, len(m.opts.Tools), output)
		m.blocks = append(m.blocks, block{kind: blockSystem, text: text})
		m.input.Reset()
		m.layout()
		m.syncViewport(false)
	case "/tools":
		var b strings.Builder
		if len(m.opts.Tools) == 0 {
			b.WriteString("no tools are registered")
		} else {
			fmt.Fprintf(&b, "%d tools\n", len(m.opts.Tools))
			for _, name := range m.opts.Tools {
				fmt.Fprintf(&b, "  %s\n", name)
			}
		}
		m.blocks = append(m.blocks, block{kind: blockSystem, text: strings.TrimRight(b.String(), "\n")})
		m.input.Reset()
		m.layout()
		m.syncViewport(false)
	case "/expand":
		m.expandTools = !m.expandTools
		state := "collapsed"
		if m.expandTools {
			state = "expanded"
		}
		m.blocks = append(m.blocks, block{kind: blockSystem, text: "tool output " + state})
		m.input.Reset()
		m.layout()
		m.syncViewport(false)
	case "/autoapprove", "/auto":
		if len(fields) > 1 {
			switch strings.ToLower(fields[1]) {
			case "on", "yes", "true":
				m.autoApprove = true
			case "off", "no", "false":
				m.autoApprove = false
			default:
				m.blocks = append(m.blocks, block{kind: blockError, text: "usage: /autoapprove [on|off]"})
				m.input.Reset()
				m.layout()
				m.syncViewport(false)
				return nil
			}
		} else {
			m.autoApprove = !m.autoApprove
		}
		m.agent.SetAutoApprove(m.autoApprove)
		state := "disabled"
		if m.autoApprove {
			state = "enabled"
		}
		m.blocks = append(m.blocks, block{kind: blockSystem, text: "auto-approve " + state})
		m.input.Reset()
		m.layout()
		m.syncViewport(false)
	case "/approve-edits", "/review-edits":
		state := true
		if len(fields) > 1 {
			switch strings.ToLower(fields[1]) {
			case "on", "yes", "true":
				state = true
			case "off", "no", "false":
				state = false
			default:
				m.blocks = append(m.blocks, block{kind: blockError, text: "usage: /approve-edits [on|off]"})
				m.input.Reset()
				m.layout()
				m.syncViewport(false)
				return nil
			}
		}
		m.agent.SetApproveEdits(state)
		label := "off"
		if state {
			label = "on"
		}
		m.blocks = append(m.blocks, block{kind: blockSystem, text: "edit review " + label})
		m.input.Reset()
		m.layout()
		m.syncViewport(false)
	case "/model":
		if len(fields) > 1 {
			name := strings.TrimSpace(fields[1])
			if m.opts.SetModel == nil {
				m.blocks = append(m.blocks, block{kind: blockError, text: "model switching is not available"})
			} else if err := m.opts.SetModel(name); err != nil {
				m.blocks = append(m.blocks, block{kind: blockError, text: "model: " + err.Error()})
			} else {
				m.opts.Model = name
				m.blocks = append(m.blocks, block{kind: blockSystem, text: "model: " + name})
			}
			m.input.Reset()
			m.layout()
			m.syncViewport(false)
			return nil
		}
		if m.opts.ListModels == nil {
			m.blocks = append(m.blocks, block{kind: blockSystem, text: "current model: " + m.currentModel()})
			m.input.Reset()
			m.layout()
			m.syncViewport(false)
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		models, err := m.opts.ListModels(ctx)
		cancel()
		if err != nil {
			m.blocks = append(m.blocks, block{kind: blockError, text: "models: " + err.Error()})
		} else {
			var b strings.Builder
			fmt.Fprintf(&b, "current model: %s\n", m.currentModel())
			for _, name := range models {
				mark := "  "
				if name == m.currentModel() {
					mark = "* "
				}
				b.WriteString(mark + name + "\n")
			}
			b.WriteString("\nswitch with /model <name>")
			m.blocks = append(m.blocks, block{kind: blockSystem, text: b.String()})
		}
		m.input.Reset()
		m.layout()
		m.syncViewport(false)
	case "/thinking":
		if len(fields) > 1 {
			level := strings.TrimSpace(fields[1])
			parsed, _ := llm.ParseThinkingLevel(level)
			if m.opts.SetThinking == nil {
				m.blocks = append(m.blocks, block{kind: blockError, text: "thinking level switching is not available"})
			} else if err := m.opts.SetThinking(parsed); err != nil {
				m.blocks = append(m.blocks, block{kind: blockError, text: "thinking: " + err.Error()})
			} else {
				m.opts.Thinking = parsed.String()
				m.blocks = append(m.blocks, block{kind: blockSystem, text: "thinking: " + parsed.String()})
			}
			m.input.Reset()
			m.layout()
			m.syncViewport(false)
			return nil
		}
		// No argument: show current and available levels.
		cur := m.opts.ThinkingLevel()
		var b strings.Builder
		fmt.Fprintf(&b, "current thinking: %s\navailable:  ", cur.String())
		for _, lvl := range llm.Levels() {
			if lvl == cur {
				fmt.Fprintf(&b, "*%s ", lvl.String())
			} else {
				fmt.Fprintf(&b, "%s ", lvl.String())
			}
		}
		fmt.Fprintf(&b, "(none = server default)")
			b.WriteString("\nchange with /thinking <level>")
			m.blocks = append(m.blocks, block{kind: blockSystem, text: b.String()})
			m.input.Reset()
			m.layout()
			m.syncViewport(false)
			return nil
		case "/theme":
			if len(fields) > 1 {
				mode := strings.ToLower(strings.TrimSpace(fields[1]))
				valid := map[string]bool{"auto": true, "dark": true, "light": true}
				if !valid[mode] {
					m.blocks = append(m.blocks, block{kind: blockError, text: "usage: /theme [auto|dark|light]"})
					m.input.Reset()
					m.layout()
					m.syncViewport(false)
					return nil
				}
				SetTheme(mode)
				m.opts.Theme = mode
			m.opts.Highlight = (mode != "off")
				m.blocks = append(m.blocks, block{kind: blockSystem, text: "theme: " + mode})
				m.input.Reset()
				m.layout()
				m.syncViewport(false)
				return nil
			}
			cur := m.opts.Theme
			blocks := "auto|dark|light"
			switch cur {
			case "auto":
				blocks = "*auto|dark|light"
			case "dark":
				blocks = "auto|*dark|light"
			default:
				blocks = "auto|dark|*light"
			}
			msg := fmt.Sprintf("current theme: %s (%s)\nchange with /theme [auto|dark|light]", cur, blocks)
			m.blocks = append(m.blocks, block{kind: blockSystem, text: msg})
			m.input.Reset()
			m.layout()
			m.syncViewport(false)
			return nil
		case "/compact":
			if m.running {
				return nil
			}
		ctx, cancel := context.WithCancel(context.Background())
		m.cancel = cancel
		m.running = true
		m.terminal = false
		m.runDone = false
		m.started = time.Now()
		m.status = "compacting"
		m.err = ""
		m.input.Reset()
		m.input.SetHeight(1)
		m.input.Blur()
		m.layout()
		m.syncViewport(true)
		ag := m.agent
		return tea.Batch(m.spinner.Tick, func() tea.Msg {
			summary, err := ag.Compact(ctx)
			return compactDoneMsg{summary: summary, err: err}
		})
	case "/context":
		msgs, tokens, window := m.agent.ContextInfo()
		text := fmt.Sprintf("messages  %d (window %d)\napprox    ~%d tokens", msgs, window, tokens)
		m.blocks = append(m.blocks, block{kind: blockSystem, text: text + "\n\nuse /compact to summarize the conversation"})
		m.input.Reset()
		m.layout()
		m.syncViewport(false)
	case "/undo":
		if m.opts.Undo == nil {
			m.blocks = append(m.blocks, block{kind: blockError, text: "undo is not available"})
		} else if out, err := m.opts.Undo(); err != nil {
			m.blocks = append(m.blocks, block{kind: blockError, text: "undo: " + err.Error()})
		} else {
			m.blocks = append(m.blocks, block{kind: blockSystem, text: out})
		}
		m.input.Reset()
		m.layout()
		m.syncViewport(false)
	case "/commit":
		if m.running {
			return nil
		}
		if m.opts.Commit == nil {
			m.blocks = append(m.blocks, block{kind: blockError, text: "commit is not available"})
			m.input.Reset()
			m.layout()
			m.syncViewport(false)
			return nil
		}
		message := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), cmd))
		ctx, cancel := context.WithCancel(context.Background())
		m.cancel = cancel
		m.running = true
		m.terminal = false
		m.runDone = false
		m.started = time.Now()
		m.status = "committing"
		m.err = ""
		m.input.Reset()
		m.input.SetHeight(1)
		m.input.Blur()
		m.layout()
		m.syncViewport(true)
		commit := m.opts.Commit
		return tea.Batch(m.spinner.Tick, func() tea.Msg {
			out, err := commit(ctx, message)
			return commitDoneMsg{output: out, err: err}
		})
	case "/session":
		m.blocks = append(m.blocks, block{kind: blockSystem, text: m.sessionInfo()})
		m.input.Reset()
		m.layout()
		m.syncViewport(false)
	case "/sessions":
		m.blocks = append(m.blocks, block{kind: blockSystem, text: m.sessionList()})
		m.input.Reset()
		m.layout()
		m.syncViewport(false)
	case "/new":
		m.newSession()
		m.input.Reset()
		m.layout()
		m.syncViewport(true)
	case "/resume":
		name := ""
		if len(fields) > 1 {
			name = fields[1]
		}
		m.resumeSession(name)
		m.input.Reset()
		m.layout()
		m.syncViewport(true)
	case "/mode":
		if len(fields) > 1 {
			mi := parseModeName(strings.ToLower(fields[1]))
			if mi < 0 {
				m.blocks = append(m.blocks, block{
					kind: blockError,
					text: fmt.Sprintf("unknown mode %q. Use: general, planner, builder, debugger, cybersecurity, simplifier (or Tab to cycle)", fields[1]),
				})
				m.input.Reset()
				m.layout()
				m.syncViewport(false)
				return nil
			}
			m.switchMode(mi)
		} else {
			m.switchMode(m.modeIdx)
		}
		m.input.Reset()
		m.layout()
		m.syncViewport(false)
	case "/mcp":
		out := "no mcp servers are configured"
		if m.mgr != nil {
			out = m.mgr.Command(fields)
		}
		m.blocks = append(m.blocks, block{kind: blockSystem, text: out})
		m.input.Reset()
		m.layout()
		m.syncViewport(false)
	case "/skills":
		out := "no skill manager is available"
		if m.skillsMgr != nil {
			out = m.skillsMgr.Command(context.Background(), fields)
		}
		m.blocks = append(m.blocks, block{kind: blockSystem, text: out})
		m.input.Reset()
		m.layout()
		m.syncViewport(false)
	case "/help", "/?", "/":
		m.blocks = append(m.blocks, block{kind: blockSystem, text: helpText})
		m.input.Reset()
		m.layout()
		m.syncViewport(false)
	default:
		m.blocks = append(m.blocks, block{kind: blockError, text: fmt.Sprintf("unknown command %q — type / to list commands", cmd)})
		m.input.Reset()
		m.layout()
		m.syncViewport(false)
	}
	return nil
}

const helpText = `Commands
  /help              show this help
  /clear             clear the transcript
  /reset             clear the transcript and forget the conversation
  /status            show provider, model, project, and approval mode
  /tools             list the available tools
  /expand            toggle full tool output
  /session           show the current session and its file
  /sessions          list recent sessions for this directory
  /new               start a fresh session
  /resume [id]       resume the latest (or named) session
  /autoapprove [on|off] toggle or set automatic approval
  /approve-edits [on|off] review file edits with a diff first
  /model [name]      list models or switch the active model
  /thinking [level]  list thinking levels or set reasoning effort
  /theme [auto|dark|light]  switch the color palette
  /compact           summarize the conversation to free context
  /context           show context usage
  /undo              restore files changed by the previous turn
  /commit [message]  stage everything and commit (generates a message)
  /mcp [subcommand]  list/add/remove/reconnect MCP servers
  /skills [subcommand]  list/info/use/run skills
  /mode [name]       switch operating mode (or press Tab to cycle)
  /quit              leave Metis

Type / to open the command menu; use ↑/↓ to choose and Tab or Enter to
complete. Esc dismisses the menu.

Keys
  Enter              send the message
  Tab                cycle the operating mode (General, Planner, Builder,
                    Debugger, Cybersecurity, Simplifier)
  Shift+Tab          cycle the thinking level (off, low, medium, high, xhigh)
  Alt+Enter / Ctrl+J insert a newline
  Ctrl+P / Ctrl+N    previous / next input in history
  PgUp / PgDn        scroll the transcript (mouse wheel works too)
  Ctrl+O             expand / collapse tool output
  Esc / Ctrl+C       cancel the running task
  Ctrl+C (idle)      quit
  Ctrl+D             quit when the input is empty
  Ctrl+L             clear the transcript
  Ctrl+R             reset the conversation

Approval
  Dangerous calls (shell, deletes, outside-project paths, downloads, bulk
  edits, non-GET HTTP) ask first. When prompted:
  y                  allow this call once
  a                  always allow this tool for the session
  A                  auto-approve every dangerous call for the session
  n / Esc            deny the call and cancel the step`
