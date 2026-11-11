package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

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
	rightText := oneLine(clean(m.currentModel() + " · " + server))
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
		thinking := "none"
		if m.opts.SetThinking != nil && m.opts.ThinkingLevel != nil {
			thinking = m.opts.ThinkingLevel().String()
		}
		right = styleDim.Render(fmt.Sprintf("step %d  ·  %s  ·  %s  ·  %s  ·  %s  ·  esc cancel",
			m.step, fmtDuration(time.Since(m.started)), modeLabel, thinking, m.contextMeter()))
	case m.err != "":
		msg := oneLine(clean(m.err))
		left = styleError.Render("● error") + "  " +
			styleText.Render(truncate(msg, max(10, m.width-32)))
		right = styleDim.Render("last run failed")
	case m.status == "cancelled":
		left = styleDim.Render("○ cancelled")
	default:
		modeLabel := modes[m.modeIdx].label
		thinking := "none"
		if m.opts.SetThinking != nil && m.opts.ThinkingLevel != nil {
			thinking = m.opts.ThinkingLevel().String()
		}
		left = styleOK.Render("●") + " " + styleStatus.Render("ready") + "  " +
			styleStatusOn.Render("· "+modeLabel) + "  " + styleDim.Render(thinking)
		if m.step > 0 {
			steps := "steps"
			if m.step == 1 {
				steps = "step"
			}
			right = styleDim.Render(fmt.Sprintf("%d %s  ·  %s  ·  %s  ·  tab mode · shift+tab thinking",
				m.step, steps, fmtDuration(m.elapsed), m.contextMeter()))
		} else {
			right = styleDim.Render(m.contextMeter() + "  ·  tab mode · shift+tab thinking · esc cancel")
		}
	}
	return " " + joinLR(left, right, max(10, m.width-2)) + " "
}

func (m *Model) inputView() string {
	rule := styleRule.Render(strings.Repeat("─", m.width))
	return rule + "\n" + styleComposer.Render(m.input.View()) + "\n" + rule
}

func (m *Model) footerView() string {
	hint := "enter send  ·  tab mode  ·  alt+enter newline  ·  /help  ·  ctrl+c quit"
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

// contextMeter renders message usage against the configured window.
func (m *Model) contextMeter() string {
	msgs, _, window := m.agent.ContextInfo()
	if window <= 0 {
		return fmt.Sprintf("ctx %d", msgs)
	}
	return fmt.Sprintf("ctx %d/%d", msgs, window)
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
