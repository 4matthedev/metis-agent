// Package tui implements Metis's interactive terminal interface.
package tui

import (
	"context"
	"errors"
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
	// Theme is the current color palette name (auto, dark, light).
	Theme string
	// AutoApprove runs dangerous tool calls without asking.
	AutoApprove bool
	// Mode is the initial operating mode (0-based index into modes). -1
	// means General. Tab cycles through modes live during the session.
	Mode int
	// Thinking is the initial thinking level (reasoning effort).
	Thinking string
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
	// Undo restores the last checkpoint (/undo).
	Undo func() (string, error)
	// Commit stages changes and creates a commit, optionally generating the
	// message when the argument is empty (/commit).
	Commit func(ctx context.Context, message string) (string, error)
	// ListModels, SetModel, and ModelName power runtime model switching.
	ListModels func(ctx context.Context) ([]string, error)
	SetModel   func(string) error
	ModelName  func() string
	// SetThinking and ThinkingLevel power runtime thinking-level switching.
	SetThinking func(llm.ThinkingLevel) error
	ThinkingLevel func() llm.ThinkingLevel
}

// modes is the ordered list of operating modes the user can switch between
// with the Tab key. Each entry is a human-friendly label; its index into
// this slice is the agent.Mode value (0-based).
var modes = []struct {
	label string
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

type compactDoneMsg struct {
	summary string
	err     error
}

type commitDoneMsg struct {
	output string
	err    error
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

	m.addWelcome()
	if m.sess != nil && m.agent.HistoryLen() > 0 {
		m.replayHistory()
	}
	m.layout()
	m.syncViewport(true)
	return m
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

	case compactDoneMsg:
		m.running = false
		m.cancel = nil
		m.terminal = true
		m.runDone = true
		m.status = "ready"
		if msg.err != nil {
			m.blocks = append(m.blocks, block{kind: blockError, text: "compact: " + msg.err.Error()})
		} else {
			m.blocks = append(m.blocks, block{
				kind: blockSystem,
				text: "conversation compacted into a summary\n\n" + msg.summary,
			})
			m.saveSession()
		}
		m.syncViewport(true)
		return m, m.input.Focus()

	case commitDoneMsg:
		m.running = false
		m.cancel = nil
		m.terminal = true
		m.runDone = true
		m.status = "ready"
		if msg.err != nil {
			m.blocks = append(m.blocks, block{kind: blockError, text: "commit: " + msg.err.Error()})
		} else {
			m.blocks = append(m.blocks, block{kind: blockSystem, text: msg.output})
		}
		m.syncViewport(true)
		return m, m.input.Focus()

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
	// Shift+Tab cycles the thinking level.
	if msg.String() == "shift+tab" && m.input.Value() == "" && m.suggestions() == nil {
		if !m.running {
			m.cycleThinking()
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

func (m *Model) cycleThinking() {
	if m.opts.SetThinking == nil {
		return
	}
	cur := m.opts.ThinkingLevel()
	levels := llm.Levels()
	// Find current index
	idx := 0
	for i, l := range levels {
		if l == cur {
			idx = i
			break
		}
	}
	next := levels[(idx+1) % len(levels)]
	if err := m.opts.SetThinking(next); err != nil {
		return
	}
	m.opts.Thinking = next.String()
	m.syncViewport(false)
}

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
