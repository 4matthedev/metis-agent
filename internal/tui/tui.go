// Package tui implements Metis's interactive terminal interface.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"metis-agent/internal/agent"
	"metis-agent/internal/llm"
	"metis-agent/internal/mcp"
	"metis-agent/internal/session"
	"metis-agent/internal/skills"
	"metis-agent/internal/tools"
)

// Options configures the interface chrome.
type Options struct {
	Model    string
	Server   string
	Provider string
	Dir      string
	Tools    []string
	// Highlight enables syntax highlighting for file previews.
	Highlight bool
	// AutoApprove runs dangerous tool calls without asking.
	AutoApprove bool
	// Mode is the initial operating mode (0-based index into modes). -1
	// means General. Tab cycles through modes live during the session.
	Mode int
	// Session fields are optional; when Store and Session are nil the TUI
	// runs without persistence.
	SessionID       string
	SessionPath     string
	SessionsEnabled bool
	Store           *session.Store
	Session         *session.Session
	// MCPManager manages MCP servers at runtime (/mcp command).
	MCPManager *mcp.Manager
	// SkillsManager manages the skill system at runtime (/skills command).
	SkillsManager *skills.Manager
}
// modes is the ordered list of operating modes the user can switch between
// with the Tab key. Each entry is a human-friendly label; its index into
// this slice is the agent.Mode value (0-based).
var modes = []struct {
	label      string
	agent.Mode
}{
	{"General", agent.ModeGeneral},
	{"Planner", agent.ModePlanner},
	{"Builder", agent.ModeBuilder},
	{"Debugger", agent.ModeDebugger},
	{"Cybersecurity", agent.ModeCybersecurity},
	{"Simplifier", agent.ModeSimplifier},
}

// currentModeIdx returns the 0-based index of m in the modes slice, or 0
// (General) when m is unknown.
func currentModeIdx(m agent.Mode) int {
	for i, mv := range modes {
		if mv.Mode == m {
			return i
		}
	}
	return 0
}

type blockKind int

const (
	blockUser blockKind = iota
	blockAssistant
	blockTool
	blockError
	blockSystem
	blockWelcome
)

type block struct {
	kind      blockKind
	text      string
	tool      string
	args      string
	result    string
	duration  string
	running   bool
	streaming bool

	// cache holds the rendered form of a completed block. Rendering Markdown,
	// syntax highlighting, and diffs is comparatively expensive and the
	// transcript is rebuilt on every token, so successful renders are reused
	// until the block or the layout changes.
	cacheKey string
	cache    string
}

// AgentEventMsg wraps an agent progress event for the Bubble Tea loop.
type AgentEventMsg agent.Event

type runDoneMsg struct {
	answer  string
	err     error
	elapsed time.Duration
}

// approvalRequestMsg asks the UI to confirm a dangerous tool call. The agent
// goroutine blocks on resp until the user answers.
type approvalRequestMsg struct {
	req  tools.ApprovalRequest
	resp chan tools.ApprovalDecision
}

type pendingApproval struct {
	req  tools.ApprovalRequest
	resp chan tools.ApprovalDecision
}

// Model is the Bubble Tea model for the Metis TUI.
type Model struct {
	agent  *agent.Agent
	opts   Options
	width  int
	height int

	viewport viewport.Model
	input    textarea.Model
	spinner  spinner.Model

	blocks          []block
	activeAssistant int

	running  bool
	terminal bool
	runDone  bool
	cancel   context.CancelFunc
	started  time.Time
	elapsed  time.Duration
	step     int
	status   string
	err      string

	history []string
	histPos int

	expandTools bool

	suggestSel       int
	suggestDismissed bool

	pendingApproval *pendingApproval
	autoApprove     bool
	modeIdx         int

	store          *session.Store
	sess           *session.Session
	sessionErrOnce bool
	mgr            *mcp.Manager
	skillsMgr      *skills.Manager
}

// Run starts the TUI and blocks until the user quits.
func Run(a *agent.Agent, opts Options) error {
	m := newModel(a, opts)
	p := tea.NewProgram(
		m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	// Route agent progress into the UI. p.Send is safe to call from the
	// goroutine that runs Agent.Run.
	a.SetEventSink(func(ev agent.Event) { p.Send(AgentEventMsg(ev)) })
	a.SetAutoApprove(opts.AutoApprove)
	a.SetApprover(func(ctx context.Context, req tools.ApprovalRequest) (tools.ApprovalDecision, error) {
		resp := make(chan tools.ApprovalDecision, 1)
		p.Send(approvalRequestMsg{req: req, resp: resp})
		select {
		case d := <-resp:
			return d, nil
		case <-ctx.Done():
			return tools.ApprovalDeny, ctx.Err()
		}
	})

	_, err := p.Run()
	return err
}

func newModel(a *agent.Agent, opts Options) *Model {
	ta := textarea.New()
	ta.Placeholder = "Ask Metis to do something…"
	ta.Prompt = "› "
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Text = styleText
	ta.FocusedStyle.Placeholder = styleDim
	ta.FocusedStyle.Prompt = styleCursor.Bold(true)
	ta.BlurredStyle = ta.FocusedStyle
	// Enter submits; Alt+Enter (or Ctrl+J) inserts a newline.
	ta.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j")

	vp := viewport.New(80, 20)
	vp.MouseWheelEnabled = true

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colAccent)

	m := &Model{
		agent:           a,
		opts:            opts,
		width:           80,
		height:          24,
		viewport:        vp,
		input:           ta,
		spinner:         sp,
		activeAssistant: -1,
		status:          "ready",
		autoApprove:     opts.AutoApprove,
		store:           opts.Store,
		sess:            opts.Session,
		mgr:             opts.MCPManager,
		skillsMgr:       opts.SkillsManager,
	}
	m.modeIdx = currentModeIdx(a.Mode())
	if opts.Mode >= 0 && opts.Mode < len(modes) {
		m.modeIdx = opts.Mode
		a.SetMode(modes[opts.Mode].Mode)
	}
	m.layout()
	m.syncViewport(true)

	m.addWelcome()
	if m.sess != nil && m.agent.HistoryLen() > 0 {
		m.replayHistory()
	}
	m.layout()
	m.syncViewport(true)
	return m
}

func (m *Model) addWelcome() {
	m.blocks = append(m.blocks, block{kind: blockWelcome})
}

// replayHistory turns a resumed conversation into transcript blocks. Tool
// traffic is not replayed; the user and assistant prose is enough to restore
// context visually while the full history remains available to the model.
func (m *Model) replayHistory() {
	hist := m.agent.ExportHistory()
	replayed := make([]block, 0, len(hist)+1)
	for _, msg := range hist {
		switch msg.Role {
		case "user":
			replayed = append(replayed, block{kind: blockUser, text: msg.Content})
		case "assistant":
			if strings.TrimSpace(msg.Content) != "" {
				replayed = append(replayed, block{kind: blockAssistant, text: msg.Content})
			}
		}
	}
	if m.sess != nil {
		replayed = append(replayed, block{
			kind: blockSystem,
			text: "resumed session " + m.sess.ID + "  (" + fmt.Sprintf("%d messages", len(hist)) + ")",
		})
	}
	if len(replayed) == 0 {
		m.blocks = nil
		m.addWelcome()
		return
	}
	m.blocks = replayed
}

// saveSession persists the current conversation after a successful turn.
func (m *Model) saveSession() {
	if m.store == nil || m.sess == nil {
		return
	}
	m.sess.Provider = m.opts.Provider
	m.sess.Model = m.opts.Model
	m.sess.WorkDir = m.opts.Dir
	m.sess.Messages = m.agent.ExportHistory()
	if err := m.store.Save(m.sess); err != nil {
		if !m.sessionErrOnce {
			m.sessionErrOnce = true
			m.blocks = append(m.blocks, block{kind: blockSystem, text: "session could not be saved: " + err.Error()})
		}
		return
	}
	m.opts.SessionID = m.sess.ID
	m.opts.SessionPath = m.store.Path(m.sess)
}

// resetSessionHistory clears both the model's memory and the saved session.
func (m *Model) resetSessionHistory() {
	m.agent.Reset()
	if m.sess != nil {
		m.sess.Messages = nil
		m.saveSession()
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.input.Focus(), tea.SetWindowTitle("metis"))
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		m.syncViewport(false)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd

	case spinner.TickMsg:
		if !m.running {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case approvalRequestMsg:
		if !m.running || m.pendingApproval != nil {
			msg.resp <- tools.ApprovalDeny
			return m, nil
		}
		m.pendingApproval = &pendingApproval{req: msg.req, resp: msg.resp}
		m.blocks = append(m.blocks, block{
			kind: blockSystem,
			text: formatApprovalRequest(msg.req),
		})
		m.status = "approval needed"
		m.input.Blur()
		m.syncViewport(true)
		return m, nil

	case AgentEventMsg:
		if !m.running && m.runDone {
			return m, nil
		}
		m.handleEvent(agent.Event(msg))
		return m, nil

	case runDoneMsg:
		m.pendingApproval = nil
		m.elapsed = msg.elapsed
		m.runDone = true
		if !m.terminal {
			// Defensive: make sure the run is finalized even if the
			// terminal event was somehow lost.
			m.terminal = true
			if msg.err != nil {
				m.addError(msg.err)
			} else if strings.TrimSpace(msg.answer) != "" && m.activeAssistant < 0 {
				m.blocks = append(m.blocks, block{kind: blockAssistant, text: msg.answer})
			}
		}
		if msg.err == nil {
			m.saveSession()
		}
		m.finalizeAssistant()
		cmd := m.maybeFinish()
		m.syncViewport(false)
		return m, cmd
	}

	if !m.running {
		var cmd tea.Cmd
		prevHeight := m.input.Height()
		m.input, cmd = m.input.Update(msg)
		if m.input.Height() != prevHeight || m.calcInputHeight() != prevHeight {
			m.layout()
			m.syncViewport(false)
		}
		return m, cmd
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pendingApproval != nil {
		return m.handleApprovalKey(msg)
	}
	// Tab cycles the operating mode. When a slash suggestion menu is open,
	// Tab is used to complete the suggestion, so we only cycle modes when no
	// suggestion menu is active and the input is empty.
	if msg.String() == "tab" && m.input.Value() == "" && m.suggestions() == nil {
		if !m.running {
			m.cycleMode()
		}
		return m, nil
	}
	if suggestions := m.suggestions(); len(suggestions) > 0 {
		switch msg.String() {
		case "up":
			m.suggestSel--
			if m.suggestSel < 0 {
				m.suggestSel = len(suggestions) - 1
			}
			return m, nil
		case "down":
			m.suggestSel++
			if m.suggestSel >= len(suggestions) {
				m.suggestSel = 0
			}
			return m, nil
		case "tab":
			m.completeSuggestion(suggestions)
			return m, nil
		case "esc":
			m.suggestDismissed = true
			m.layout()
			m.syncViewport(false)
			return m, nil
		case "enter":
			// Enter on a partial command completes it; on a complete command
			// it falls through and runs it.
			if !isExactCommand(strings.TrimSpace(m.input.Value())) {
				m.completeSuggestion(suggestions)
				return m, nil
			}
		}
	}
	switch msg.String() {
	case "ctrl+c":
		if m.running {
			m.cancelRun()
			return m, nil
		}
		m.saveSession()
		return m, tea.Quit

	case "esc":
		if m.running {
			m.cancelRun()
		}
		return m, nil

	case "ctrl+d":
		if !m.running && strings.TrimSpace(m.input.Value()) == "" {
			m.saveSession()
			return m, tea.Quit
		}
		if m.running {
			m.cancelRun()
		}
		return m, nil

	case "ctrl+l":
		if !m.running {
			m.blocks = nil
			m.addWelcome()
			m.err = ""
			m.status = "ready"
			m.activeAssistant = -1
			m.syncViewport(true)
		}
		return m, nil

	case "ctrl+r":
		if !m.running {
			m.resetSessionHistory()
			m.blocks = nil
			m.addWelcome()
			m.err = ""
			m.status = "ready"
			m.elapsed = 0
			m.step = 0
			m.activeAssistant = -1
			m.syncViewport(true)
		}
		return m, nil

	case "ctrl+o":
		m.expandTools = !m.expandTools
		m.syncViewport(false)
		return m, nil

	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd

	case "enter":
		if m.running {
			return m, nil
		}
		return m, m.submit()

	case "alt+enter", "ctrl+j":
		if !m.running {
			m.input.InsertString("\n")
			m.layout()
			m.syncViewport(false)
		}
		return m, nil

	case "ctrl+p":
		if !m.running {
			m.prevHistory()
			m.layout()
			m.syncViewport(false)
		}
		return m, nil

	case "ctrl+n":
		if !m.running {
			m.nextHistory()
			m.layout()
			m.syncViewport(false)
		}
		return m, nil
	}

	if m.running {
		return m, nil
	}
	prevHeight := m.calcInputHeight()
	prevPalette := m.paletteHeight()
	prevValue := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != prevValue {
		m.suggestDismissed = false
		m.suggestSel = 0
	}
	if m.calcInputHeight() != prevHeight || m.paletteHeight() != prevPalette {
		m.layout()
		m.syncViewport(false)
	}
	return m, cmd
}

func (m *Model) switchMode(idx int) {
	if idx < 0 || idx >= len(modes) {
		return
	}
	m.modeIdx = idx
	m.agent.SetMode(modes[idx].Mode)
	m.blocks = append(m.blocks, block{
		kind: blockSystem,
		text: "mode: " + modes[idx].label,
	})
	m.layout()
	m.syncViewport(false)
}

func (m *Model) cycleMode() {
	m.modeIdx = (m.modeIdx + 1) % len(modes)
	m.switchMode(m.modeIdx)
}

func (m *Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.pendingApproval
	decision := tools.ApprovalDeny
	label := "denied"
	cancel := false
	switch msg.String() {
	case "y", "Y":
		decision = tools.ApprovalOnce
		label = "approved once"
	case "a":
		decision = tools.ApprovalAlways
		label = "always allowed for this session"
	case "A":
		decision = tools.ApprovalAll
		label = "auto-approve enabled"
		m.autoApprove = true
		m.agent.SetAutoApprove(true)
	case "n", "N":
		decision = tools.ApprovalDeny
		label = "denied"
	case "esc", "ctrl+c":
		decision = tools.ApprovalDeny
		label = "denied"
		cancel = true
	default:
		return m, nil
	}
	p.resp <- decision
	m.pendingApproval = nil
	m.blocks = append(m.blocks, block{
		kind: blockSystem,
		text: "approval · " + p.req.Tool + " · " + label,
	})
	if cancel {
		m.cancelRun()
	} else {
		m.status = "thinking"
		m.syncViewport(false)
		return m, m.input.Focus()
	}
	m.syncViewport(false)
	return m, nil
}

func formatApprovalRequest(req tools.ApprovalRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "approval required · %s\n", req.Tool)
	if req.Summary != "" {
		fmt.Fprintf(&b, "  %s\n", oneLine(req.Summary))
	}
	if req.Reason != "" {
		fmt.Fprintf(&b, "  %s\n", req.Reason)
	}
	fmt.Fprintf(&b, "  y allow once   a always allow %s   A auto-approve all   n deny", req.Tool)
	return b.String()
}

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
	{"/mcp", "[list|add|remove|reconnect]", "manage MCP servers"},
	{"/mode", "[name]", "switch operating mode (general/planner/builder/debugger/cybersecurity/simplifier); press Tab to cycle"},
	{"/skills", "[list|info|use|run]", "manage the skill system"},
	{"/quit", "", "leave Metis"},
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
		text := fmt.Sprintf("provider  %s\nmodel     %s\nserver    %s\nproject   %s\napproval  %s\ntools     %d\noutput    %s",
			m.opts.Provider, m.opts.Model, m.opts.Server, m.opts.Dir, approval, len(m.opts.Tools), output)
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

func (m *Model) sessionsOn() bool {
	return m.store != nil && m.sess != nil
}

func (m *Model) sessionInfo() string {
	if !m.sessionsOn() {
		return "sessions are disabled (start with --session NAME or --resume)"
	}
	msgs := m.agent.HistoryLen()
	return fmt.Sprintf("session  %s\npath     %s\nmessages %d", m.sess.ID, m.store.Path(m.sess), msgs)
}

func (m *Model) sessionList() string {
	if m.store == nil {
		return "sessions are disabled (start with --session NAME or --resume)"
	}
	infos, err := m.store.List(m.opts.Dir, 10)
	if err != nil {
		return "could not list sessions: " + err.Error()
	}
	if len(infos) == 0 {
		return "no saved sessions for this directory"
	}
	var b strings.Builder
	b.WriteString("recent sessions\n")
	for _, info := range infos {
		marker := "  "
		if m.sess != nil && info.ID == m.sess.ID {
			marker = "* "
		}
		when := info.UpdatedAt.Local().Format("2006-01-02 15:04")
		desc := strings.TrimSpace(info.Provider + " " + info.Model)
		if desc != "" {
			desc = "  " + desc
		}
		fmt.Fprintf(&b, "%s%-24s %s%s\n", marker, info.ID, when, desc)
		if info.Preview != "" {
			fmt.Fprintf(&b, "    %s\n", truncate(info.Preview, max(20, m.width-8)))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) newSession() {
	if m.store == nil {
		m.blocks = append(m.blocks, block{kind: blockSystem, text: "sessions are disabled"})
		return
	}
	m.agent.Reset()
	m.sess = m.store.New(m.opts.Dir, m.opts.Provider, m.opts.Model)
	m.opts.SessionID = m.sess.ID
	m.opts.SessionPath = m.store.Path(m.sess)
	m.blocks = nil
	m.addWelcome()
	m.err = ""
	m.status = "ready"
	m.elapsed = 0
	m.step = 0
	m.activeAssistant = -1
	m.saveSession()
}

func (m *Model) resumeSession(name string) {
	if m.store == nil {
		m.blocks = append(m.blocks, block{kind: blockSystem, text: "sessions are disabled"})
		return
	}
	var (
		sess *session.Session
		err  error
	)
	if name == "" {
		sess, err = m.store.Latest(m.opts.Dir)
	} else {
		sess, err = m.store.Load(m.opts.Dir, name)
	}
	if err != nil {
		m.blocks = append(m.blocks, block{kind: blockError, text: "could not resume session: " + err.Error()})
		return
	}
	m.sess = sess
	m.opts.SessionID = sess.ID
	m.opts.SessionPath = m.store.Path(sess)
	m.agent.LoadHistory(sess.Messages)
	m.blocks = nil
	m.addWelcome()
	if m.agent.HistoryLen() > 0 {
		m.replayHistory()
	}
	m.err = ""
	m.status = "ready"
	m.elapsed = 0
	m.step = 0
	m.activeAssistant = -1
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
  /mcp [subcommand]  list/add/remove/reconnect MCP servers
  /skills [subcommand]  list/info/use/run skills
  /quit              leave Metis

Type / to open the command menu; use ↑/↓ to choose and Tab or Enter to
complete. Esc dismisses the menu.

Keys
  Enter              send the message
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

func (m *Model) cancelRun() {
	if m.pendingApproval != nil {
		m.pendingApproval.resp <- tools.ApprovalDeny
		m.pendingApproval = nil
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.status = "cancelling"
}

func (m *Model) handleEvent(ev agent.Event) {
	switch ev.Kind {
	case agent.EventStep:
		m.finalizeAssistant()
		m.step = ev.Step
		m.status = "thinking"
		m.syncViewport(false)

	case agent.EventText:
		if m.activeAssistant < 0 {
			m.blocks = append(m.blocks, block{kind: blockAssistant, streaming: true})
			m.activeAssistant = len(m.blocks) - 1
		}
		m.blocks[m.activeAssistant].text += ev.Text
		m.status = "writing"
		m.syncViewport(false)

	case agent.EventToolCall:
		m.finalizeAssistant()
		m.blocks = append(m.blocks, block{
			kind:    blockTool,
			tool:    ev.Tool,
			args:    ev.Args,
			running: true,
		})
		m.status = "running " + ev.Tool
		m.syncViewport(false)

	case agent.EventToolResult:
		for i := len(m.blocks) - 1; i >= 0; i-- {
			if m.blocks[i].kind == blockTool && m.blocks[i].running {
				m.blocks[i].running = false
				m.blocks[i].result = ev.Result
				m.blocks[i].cacheKey = ""
				if ev.Duration > 0 {
					m.blocks[i].duration = fmtDuration(ev.Duration)
				}
				break
			}
		}
		m.status = "thinking"
		m.syncViewport(false)

	case agent.EventDone:
		if m.activeAssistant < 0 && strings.TrimSpace(ev.Text) != "" {
			m.blocks = append(m.blocks, block{kind: blockAssistant, text: ev.Text})
		}
		m.finalizeAssistant()
		m.terminal = true
		m.status = "finishing"
		m.syncViewport(false)

	case agent.EventError:
		m.finalizeAssistant()
		m.terminal = true
		m.addError(ev.Err)
		m.syncViewport(false)
	}
}

func (m *Model) addError(err error) {
	if err == nil {
		return
	}
	if errors.Is(err, context.Canceled) {
		m.blocks = append(m.blocks, block{kind: blockSystem, text: "cancelled"})
		m.status = "cancelled"
		return
	}
	msg := friendlyError(err)
	m.err = msg
	m.status = "error"
	m.blocks = append(m.blocks, block{kind: blockError, text: msg})
}

func (m *Model) maybeFinish() tea.Cmd {
	if !m.running || !m.runDone || !m.terminal {
		return nil
	}
	m.running = false
	m.cancel = nil
	if m.err == "" && m.status != "cancelled" {
		m.status = "ready"
	}
	cmd := m.input.Focus()
	m.syncViewport(false)
	return cmd
}

func (m *Model) finalizeAssistant() {
	if m.activeAssistant >= 0 && m.activeAssistant < len(m.blocks) {
		m.blocks[m.activeAssistant].streaming = false
	}
	m.activeAssistant = -1
}

func (m *Model) prevHistory() {
	if len(m.history) == 0 {
		return
	}
	if m.histPos > 0 {
		m.histPos--
	}
	m.input.SetValue(m.history[m.histPos])
	m.input.CursorEnd()
	m.suggestDismissed = false
	m.suggestSel = 0
}

func (m *Model) nextHistory() {
	if len(m.history) == 0 {
		return
	}
	if m.histPos < len(m.history)-1 {
		m.histPos++
		m.input.SetValue(m.history[m.histPos])
	} else {
		m.histPos = len(m.history)
		m.input.Reset()
	}
	m.input.CursorEnd()
	m.suggestDismissed = false
	m.suggestSel = 0
}

func (m *Model) calcInputHeight() int {
	w := m.input.Width()
	if w <= 0 {
		w = 70
	}
	h := 0
	for _, line := range strings.Split(m.input.Value(), "\n") {
		lw := lipgloss.Width(line)
		n := 1
		if lw > w {
			n = (lw + w - 1) / w
		}
		h += n
	}
	if h < 1 {
		h = 1
	}
	if h > 8 {
		h = 8
	}
	return h
}

func (m *Model) layout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	inputWidth := m.width - 2
	if inputWidth < 20 {
		inputWidth = 20
	}
	m.input.SetWidth(inputWidth)
	m.input.SetHeight(m.calcInputHeight())

	const headerH, statusH, footerH = 2, 1, 1
	inputBoxH := m.input.Height() + 2 // top and bottom rules
	vh := m.height - headerH - statusH - footerH - inputBoxH - m.paletteHeight()
	if vh < 3 {
		vh = 3
	}
	m.viewport.Width = m.width
	m.viewport.Height = vh
}

func (m *Model) syncViewport(forceBottom bool) {
	follow := forceBottom || m.viewport.AtBottom()
	m.viewport.SetContent(m.transcript())
	if follow {
		m.viewport.GotoBottom()
	}
}

func (m *Model) transcript() string {
	if len(m.blocks) == 0 {
		return ""
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	var b strings.Builder
	for i := range m.blocks {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.renderBlock(&m.blocks[i], width))
	}
	return b.String()
}

func (m *Model) renderBlock(b *block, width int) string {
	switch b.kind {
	case blockUser:
		return renderChat("you", styleUser, b.text, width-1, false)
	case blockAssistant:
		if b.streaming {
			return renderAssistant(b.text, width-1, true)
		}
		key := fmt.Sprintf("md:%d:%t", width, darkBackground())
		if b.cacheKey == key {
			return b.cache
		}
		b.cacheKey = key
		b.cache = renderAssistant(b.text, width-1, false)
		return b.cache
	case blockTool:
		maxLines := 3
		switch b.tool {
		case "file_edit", "file_write", "multi_edit", "replace_in_files",
			"diff_files", "git_diff", "git_status", "git_log", "git_show", "git_blame":
			maxLines = 12 // show diffs and history by default
		case "file_read_many", "project_info", "json_query", "http_fetch",
			"todo_write", "todo_read", "system_info", "env_get", "which", "port_check":
			maxLines = 8
		}
		if m.expandTools {
			maxLines = 200
		}
		key := fmt.Sprintf("tool:%d:%d:%t:%t:%d", width, maxLines, b.running, m.opts.Highlight, len(b.result))
		if b.cacheKey == key {
			return b.cache
		}
		b.cacheKey = key
		b.cache = renderTool(b.tool, b.args, b.result, b.duration, b.running, width-1, maxLines, m.opts.Highlight)
		return b.cache
	case blockError:
		return renderError(b.text, width-1)
	case blockSystem:
		return renderSystem(b.text, width-1)
	case blockWelcome:
		return renderWelcome(m.opts, width-1)
	default:
		return ""
	}
}

func (m *Model) View() string {
	if m.width < 30 || m.height < 12 {
		return "metis needs at least 30×12.\nResize the terminal to continue."
	}
	parts := []string{
		m.headerView(),
		m.viewport.View(),
		m.statusView(),
	}
	if palette := m.suggestionView(); palette != "" {
		parts = append(parts, palette)
	}
	parts = append(parts, m.inputView(), m.footerView())
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m *Model) headerView() string {
	width := max(10, m.width)
	brand := styleBrand.Render("▌ metis") + styleDim.Render("  coding agent")
	server := strings.TrimPrefix(strings.TrimPrefix(m.opts.Server, "https://"), "http://")
	rightText := oneLine(clean(m.opts.Model + " · " + server))
	right := styleHeaderSub.Render(truncate(rightText, max(5, width-lipgloss.Width(brand)-2)))
	top := joinLR(brand, right, width)
	rule := styleRule.Render(strings.Repeat("─", width))
	return top + "\n" + rule
}

func (m *Model) statusView() string {
	var left, right string
	switch {
	case m.pendingApproval != nil:
		left = styleTool.Render("approval needed")
		right = styleDim.Render("y allow · a always · A auto · n deny")
	case m.running:
		left = m.spinner.View() + " " + styleStatusOn.Render(m.status)
		modeLabel := modes[m.modeIdx].label
		right = styleDim.Render(fmt.Sprintf("step %d  ·  %s  ·  %s  ·  esc cancel",
			m.step, fmtDuration(time.Since(m.started)), modeLabel))
	case m.err != "":
		msg := oneLine(clean(m.err))
		left = styleError.Render("● error") + "  " +
			styleText.Render(truncate(msg, max(10, m.width-32)))
		right = styleDim.Render("last run failed")
	case m.status == "cancelled":
		left = styleDim.Render("○ cancelled")
	default:
		modeLabel := modes[m.modeIdx].label
		left = styleOK.Render("●") + " " + styleStatus.Render("ready") + "  " +
			styleStatusOn.Render("· " + modeLabel)
		if m.step > 0 {
			steps := "steps"
			if m.step == 1 {
				steps = "step"
			}
			right = styleDim.Render(fmt.Sprintf("%d %s  ·  %s  ·  tab mode", m.step, steps, fmtDuration(m.elapsed)))
		} else {
			right = styleDim.Render("tab mode · esc cancel")
		}
	}
	return " " + joinLR(left, right, max(10, m.width-2)) + " "
}

func (m *Model) inputView() string {
	rule := styleRule.Render(strings.Repeat("─", m.width))
	return rule + "\n" + styleComposer.Render(m.input.View()) + "\n" + rule
}

func (m *Model) footerView() string {
	hint := "enter send  ·  alt+enter newline  ·  ctrl+o tools  ·  /help  ·  ctrl+c quit"
	if m.pendingApproval != nil {
		hint = "y allow once  ·  a always allow tool  ·  A auto-approve all  ·  n deny  ·  esc cancel"
	} else if m.running {
		hint = "esc cancel  ·  ctrl+c cancel  ·  the transcript keeps updating above"
	} else if len(m.suggestions()) > 0 {
		hint = "↑↓ choose  ·  tab or enter complete  ·  esc dismiss"
	}
	if m.width < 70 {
		hint = "enter send · ctrl+o tools · /help · ctrl+c quit"
	}
	if m.width < 42 {
		hint = "enter send · /help"
	}
	return " " + styleFooter.Render(truncate(hint, max(1, m.width-2)))
}

func joinLR(left, right string, width int) string {
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	if lw+rw+1 > width {
		if rw >= width {
			return truncate(right, width)
		}
		left = truncate(left, width-rw-1)
		lw = lipgloss.Width(left)
	}
	gap := width - lw - rw
	if gap < 0 {
		gap = 0
	}
	return left + strings.Repeat(" ", gap) + right
}

func friendlyError(err error) string {
	if err == nil {
		return ""
	}
	if llm.IsModelNotLoaded(err) {
		return "MODEL NOT LOADED — load a model on the server or pick another with --model.\n" + err.Error()
	}
	if ce, ok := llm.AsClientError(err); ok {
		return ce.Error()
	}
	return err.Error()
}

func fmtDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
