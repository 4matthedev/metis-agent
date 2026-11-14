package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"metis-agent/internal/llm"
)

func (m *Model) addWelcome() {
	m.blocks = append(m.blocks, block{kind: blockWelcome})
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
	inputWidth := m.width - 4
	if inputWidth < 16 {
		inputWidth = 16
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
	m.layoutW = m.width
	m.layoutH = m.height
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
		return renderUser(b.text, width-1)
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
	case blockApproval:
		return renderApproval(b.text, width-1)
	case blockSystem:
		return renderSystem(b.text, width-1)
	case blockWelcome:
		height := m.viewport.Height
		if height <= 0 {
			height = 17
		}
		return renderWelcome(m.opts, width-1, height)
	default:
		return ""
	}
}

func (m *Model) View() string {
	if m.width < 30 || m.height < 12 {
		return "metis needs at least 30×12.\nResize the terminal to continue."
	}
	// Keep geometry coherent even when View is called directly (for example
	// from tests) without a preceding WindowSizeMsg.
	if m.layoutW != m.width || m.layoutH != m.height {
		m.layout()
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
	return fitView(lipgloss.JoinVertical(lipgloss.Left, parts...), m.width, m.height)
}

// fitView enforces the terminal bounds: every line is clipped to the width and
// the block is padded or trimmed to exactly height lines. Keeping this
// invariant means chrome changes cannot push the composer off screen.
func fitView(s string, width, height int) string {
	if width < 1 {
		width = 1
	}
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "")
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m *Model) headerView() string {
	width := max(10, m.width)
	brand := styleBrand.Render("◆ metis") + styleDim.Render("  coding agent")
	meta := projectLabel(m.opts.Dir) + " · " + m.currentModel()
	right := styleHeaderSub.Render(truncate(oneLine(clean(meta)), max(5, width-lipgloss.Width(brand)-2)))
	top := joinLR(brand, right, width)
	rule := styleRule.Render(strings.Repeat("─", width))
	return top + "\n" + rule
}

func (m *Model) modeName() string {
	if m.modeIdx >= 0 && m.modeIdx < len(modes) {
		return modes[m.modeIdx].label
	}
	return modes[0].label
}

func (m *Model) thinkingName() string {
	if m.opts.SetThinking != nil && m.opts.ThinkingLevel != nil {
		return m.opts.ThinkingLevel().String()
	}
	if m.opts.Thinking != "" {
		return m.opts.Thinking
	}
	return "none"
}

func (m *Model) statusView() string {
	var left, right string
	switch {
	case m.pendingApproval != nil:
		left = styleWarn.Render("● approval needed") + "  " +
			styleDim.Render("review the diff above")
		right = styleDim.Render("y allow · a always · A auto · n deny")
	case m.running:
		left = m.spinner.View() + " " + styleStatusOn.Render(m.status) + "  " +
			styleDim.Render(fmt.Sprintf("step %d · %s", m.step, fmtDuration(time.Since(m.started))))
		right = pill(m.modeName(), colSecondary) + " " +
			styleDim.Render(m.thinkingName()+"  ·  "+m.contextMeter()+"  ·  esc cancel")
	case m.err != "":
		left = styleError.Render("● error") + "  " +
			styleText.Render(truncate(oneLine(clean(m.err)), max(10, m.width-26)))
		right = styleDim.Render("last run failed")
	case m.status == "cancelled":
		left = styleWarn.Render("○ cancelled")
		right = styleDim.Render("enter to retry")
	default:
		left = styleOK.Render("●") + " " + styleStatus.Render("ready") + "  " +
			pill(m.modeName(), colSecondary) + " " + styleDim.Render(m.thinkingName())
		if m.step > 0 {
			steps := "steps"
			if m.step == 1 {
				steps = "step"
			}
			right = styleDim.Render(fmt.Sprintf("%d %s · %s · %s  ", m.step, steps, fmtDuration(m.elapsed), m.contextMeter())) +
				pill(m.currentModel(), colPrimary)
		} else {
			right = styleDim.Render(m.contextMeter()+"  ") + pill(m.currentModel(), colPrimary)
		}
	}
	return " " + joinLR(left, right, max(10, m.width-2)) + " "
}

func (m *Model) inputView() string {
	style := styleComposer
	if m.running || m.pendingApproval != nil {
		style = styleComposerBusy
	}
	return style.Render(m.input.View())
}

func (m *Model) footerView() string {
	hint := "enter send  ·  tab mode  ·  shift+tab thinking  ·  /help  ·  ctrl+c quit"
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

// refreshContextInfo snapshots the agent's context usage. It must only be
// called while no run is active: the agent holds its lock for the whole run,
// and View is rendered on the same goroutine as the UI loop.
func (m *Model) refreshContextInfo() {
	msgs, _, window := m.agent.ContextInfo()
	m.ctxMsgs = msgs
	m.ctxWindow = window
}

// contextMeter renders cached message usage against the configured window.
// It deliberately avoids Agent.ContextInfo: calling into the agent from View
// deadlocks as soon as a run starts.
func (m *Model) contextMeter() string {
	if m.ctxWindow <= 0 {
		return fmt.Sprintf("ctx %d", m.ctxMsgs)
	}
	return fmt.Sprintf("ctx %d/%d", m.ctxMsgs, m.ctxWindow)
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

func projectPath(dir string) string {
	if dir == "" || dir == "." {
		if wd, err := os.Getwd(); err == nil {
			return wd
		}
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

func projectLabel(dir string) string {
	path := projectPath(dir)
	if base := filepath.Base(path); base != "" && base != "." {
		return base
	}
	return path
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
