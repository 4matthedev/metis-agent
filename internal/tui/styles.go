package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Palette. A warm accent against cool secondary and structural tones, tuned
// separately for dark and light terminals.
var (
	colPrimary   = lipgloss.AdaptiveColor{Light: "#3B7DD8", Dark: "#FAB283"}
	colSecondary = lipgloss.AdaptiveColor{Light: "#7B5BB6", Dark: "#5C9CF5"}
	colAccent    = lipgloss.AdaptiveColor{Light: "#D68C27", Dark: "#9D7CD8"}
	colOK        = lipgloss.AdaptiveColor{Light: "#3D9A57", Dark: "#7FD88F"}
	colWarn      = lipgloss.AdaptiveColor{Light: "#D68C27", Dark: "#F5A742"}
	colError     = lipgloss.AdaptiveColor{Light: "#D1383D", Dark: "#E06C75"}
	colMuted     = lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#6A6A6A"}
	colText      = lipgloss.AdaptiveColor{Light: "#2A2A2A", Dark: "#E0E0E0"}
	colRule      = lipgloss.AdaptiveColor{Light: "#D3D3D3", Dark: "#4B4C5C"}
	colSel       = lipgloss.AdaptiveColor{Light: "#E5E5E6", Dark: "#303030"}
	colOnColor   = lipgloss.AdaptiveColor{Light: "#F8F8F8", Dark: "#212121"}
)

var (
	styleText      = lipgloss.NewStyle().Foreground(colText)
	styleBrand     = lipgloss.NewStyle().Foreground(colPrimary).Bold(true)
	styleLogoAgent = lipgloss.NewStyle().Foreground(colSecondary).Bold(true)
	styleHeaderSub = lipgloss.NewStyle().Foreground(colMuted)
	styleRule      = lipgloss.NewStyle().Foreground(colRule)
	styleUser      = lipgloss.NewStyle().Foreground(colSecondary).Bold(true)
	styleMetis     = lipgloss.NewStyle().Foreground(colPrimary).Bold(true)
	styleTool      = lipgloss.NewStyle().Foreground(colWarn).Bold(true)
	styleOK        = lipgloss.NewStyle().Foreground(colOK)
	styleWarn      = lipgloss.NewStyle().Foreground(colWarn).Bold(true)
	styleError     = lipgloss.NewStyle().Foreground(colError).Bold(true)
	styleDim       = lipgloss.NewStyle().Foreground(colMuted)
	styleCursor    = lipgloss.NewStyle().Foreground(colPrimary)
	styleStatus    = lipgloss.NewStyle().Foreground(colMuted)
	styleStatusOn  = lipgloss.NewStyle().Foreground(colPrimary)
	styleComposer  = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colRule).
			Padding(0, 1)
	styleComposerBusy = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colPrimary).
				Padding(0, 1)
	styleFooter  = lipgloss.NewStyle().Foreground(colMuted)
	styleSuggest = lipgloss.NewStyle().
			Foreground(colText).
			Background(colSel).
			Bold(true)

	styleDiffAdd     = lipgloss.NewStyle().Foreground(colOK)
	styleDiffDel     = lipgloss.NewStyle().Foreground(colError)
	styleDiffHunk    = lipgloss.NewStyle().Foreground(colAccent)
	styleDiffMeta    = lipgloss.NewStyle().Foreground(colMuted).Bold(true)
	styleDiffContext = lipgloss.NewStyle().Foreground(colMuted)
)

// pill renders a compact label with a filled background, used for mode,
// thinking, and model indicators in the status bar.
func pill(label string, bg lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().
		Foreground(colOnColor).
		Background(bg).
		Padding(0, 1).
		Render(label)
}

// card renders content with a thick colored rule down its left edge. The
// width argument is the total width including the rule; the content area is
// width-2 after the left padding.
func card(border lipgloss.TerminalColor, width int, content string) string {
	if width < 8 {
		return content
	}
	return lipgloss.NewStyle().
		Border(lipgloss.ThickBorder(), false, false, false, true).
		BorderForeground(border).
		PaddingLeft(1).
		Width(max(1, width-1)).
		Render(content)
}

// renderUser renders a user turn as a card with a secondary-colored edge.
func renderUser(text string, width int) string {
	text = clean(text)
	if text == "" {
		text = " "
	}
	body := ansi.Wrap(text, max(12, width-4), "")
	content := styleUser.Render("you") + "\n" + styleText.Render(body)
	return card(colSecondary, width, content)
}

// renderTool renders a tool call as a compact card:
//
//	┃ shell  $ go test ./...              ✓ ok  1.2s
//	┃   PASS
func renderTool(tool, args, result, duration string, running bool, width, maxLines int, highlight bool) string {
	tool = clean(tool)
	args = clean(args)
	result = clean(result)

	inner := max(12, width-2)
	failed := toolFailed(result)

	border := colRule
	status := styleOK.Render("✓ ok")
	switch {
	case running:
		border = colWarn
		status = styleStatusOn.Render("◌ running")
	case failed:
		border = colError
		status = styleError.Render("✗ failed")
	}
	if duration != "" && !running {
		status += styleDim.Render("  " + duration)
	}

	left := styleTool.Render(truncate(tool, max(8, inner/3)))
	if hint := formatToolArgs(tool, args); hint != "" {
		hintW := max(10, inner-lipgloss.Width(left)-lipgloss.Width(status)-3)
		left += "  " + styleDim.Render(truncate(hint, hintW))
	}
	lines := []string{joinLR(left, status, inner)}

	if running && strings.TrimSpace(result) == "" {
		lines = append(lines, styleDim.Render("working…"))
	} else if strings.TrimSpace(result) != "" {
		preview := previewLines(result, maxLines)
		isDiff := looksLikeDiff(result)
		textW := max(10, inner-2)

		var highlighted []string
		highlightedOK := false
		if highlight && !isDiff && !failed && !running && tool == "file_read" {
			if p := pathArg(args); p != "" {
				highlighted, highlightedOK = highlightPreview(preview, p)
			}
		}

		for i, line := range preview {
			if i == 0 && !failed {
				line = strings.TrimPrefix(line, "ok: ")
			}
			var text string
			switch {
			case highlightedOK && i < len(highlighted):
				text = ansi.Truncate(highlighted[i], textW, "…")
			case isDiff:
				text = renderDiffLine(line, textW)
			case failed:
				text = styleError.Render(truncate(line, textW))
			default:
				text = styleDim.Render(truncate(line, textW))
			}
			lines = append(lines, "  "+text)
		}
	}
	return card(border, width, strings.Join(lines, "\n"))
}

// pathArg extracts a file path from a tool-call argument object.
func pathArg(raw string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &args); err != nil {
		return ""
	}
	p, _ := args["path"].(string)
	return p
}

// toolFailed reports whether a tool result should be shown as a failure.
func toolFailed(result string) bool {
	s := strings.TrimSpace(result)
	if strings.HasPrefix(s, "error:") {
		return true
	}
	if strings.HasPrefix(s, "exit code:") {
		fields := strings.Fields(strings.SplitN(s, "\n", 2)[0])
		if len(fields) >= 3 && fields[2] != "0" {
			return true
		}
	}
	return false
}

// formatToolArgs turns the raw JSON arguments into a compact, readable hint.
func formatToolArgs(tool, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return oneLine(raw)
	}
	switch tool {
	case "shell":
		cmd, _ := args["command"].(string)
		cwd, _ := args["cwd"].(string)
		if cwd != "" && cwd != "." {
			return fmt.Sprintf("$ %s  (in %s)", oneLine(cmd), cwd)
		}
		return "$ " + oneLine(cmd)
	case "search":
		pat, _ := args["pattern"].(string)
		inc, _ := args["include"].(string)
		p, _ := args["path"].(string)
		s := "/" + pat + "/"
		if p != "" && p != "." {
			s += " in " + p
		}
		if inc != "" {
			s += " (" + inc + ")"
		}
		return s
	case "list_dir":
		p, _ := args["path"].(string)
		if p == "" {
			p = "."
		}
		if d, ok := args["depth"]; ok {
			return fmt.Sprintf("%s  (depth %v)", p, d)
		}
		return p
	case "glob":
		pat, _ := args["pattern"].(string)
		p, _ := args["path"].(string)
		if p != "" && p != "." {
			return pat + "  in " + p
		}
		return pat
	case "file_stat", "make_dir":
		p, _ := args["path"].(string)
		return p
	case "file_read_many":
		if paths, ok := args["paths"].([]any); ok {
			return fmt.Sprintf("%d files", len(paths))
		}
		return "files"
	case "diff_files":
		a, _ := args["a"].(string)
		b, _ := args["b"].(string)
		return a + "  vs  " + b
	case "replace_in_files":
		pat, _ := args["pattern"].(string)
		p, _ := args["path"].(string)
		s := "/" + pat + "/"
		if p != "" && p != "." {
			s += "  in " + p
		}
		if dry, _ := args["dry_run"].(bool); dry {
			s += "  (dry run)"
		}
		return s
	case "hash_file":
		p, _ := args["path"].(string)
		algo, _ := args["algorithm"].(string)
		if algo == "" {
			algo = "sha256"
		}
		return p + "  (" + algo + ")"
	case "project_info":
		p, _ := args["path"].(string)
		if p == "" {
			p = "."
		}
		return p
	case "json_query":
		if p, _ := args["path"].(string); p != "" {
			q, _ := args["query"].(string)
			if q != "" {
				return p + "  " + q
			}
			return p
		}
		if q, _ := args["query"].(string); q != "" {
			return q
		}
		return "inline JSON"
	case "git_status", "git_diff", "git_log", "git_show", "git_blame":
		if f, _ := args["file"].(string); f != "" {
			return f
		}
		if p, _ := args["path"].(string); p != "" && p != "." {
			return p
		}
		if rev, _ := args["rev"].(string); rev != "" {
			return rev
		}
		return ""
	case "download_file":
		u, _ := args["url"].(string)
		p, _ := args["path"].(string)
		return u + "  ->  " + p
	case "env_get":
		if name, _ := args["name"].(string); name != "" {
			return name
		}
		if prefix, _ := args["prefix"].(string); prefix != "" {
			return prefix + "*"
		}
		return ""
	case "which":
		if names, ok := args["commands"].([]any); ok {
			return fmt.Sprintf("%d commands", len(names))
		}
		cmd, _ := args["command"].(string)
		return cmd
	case "port_check":
		host, _ := args["host"].(string)
		if host == "" {
			host = "127.0.0.1"
		}
		if p, ok := args["port"]; ok {
			return fmt.Sprintf("%s:%v", host, p)
		}
		if ports, ok := args["ports"].([]any); ok {
			return fmt.Sprintf("%s (%d ports)", host, len(ports))
		}
		return host
	case "todo_write":
		if items, ok := args["items"].([]any); ok {
			return fmt.Sprintf("%d items", len(items))
		}
		return "update list"
	case "file_delete":
		p, _ := args["path"].(string)
		if b, _ := args["recursive"].(bool); b {
			return p + "  (recursive)"
		}
		return p
	case "file_move", "file_copy":
		from, _ := args["from"].(string)
		to, _ := args["to"].(string)
		return from + "  ->  " + to
	case "file_write":
		p, _ := args["path"].(string)
		if c, ok := args["content"].(string); ok {
			return fmt.Sprintf("%s  (%d chars)", p, len(c))
		}
		return p
	case "multi_edit":
		p, _ := args["path"].(string)
		if edits, ok := args["edits"].([]any); ok {
			return fmt.Sprintf("%s  (%d edits)", p, len(edits))
		}
		return p
	case "http_fetch":
		u, _ := args["url"].(string)
		if method, _ := args["method"].(string); method != "" && strings.ToUpper(method) != "GET" {
			return strings.ToUpper(method) + " " + u
		}
		return u
	}
	if p, ok := args["path"].(string); ok {
		return p
	}
	return oneLine(raw)
}

func renderError(text string, width int) string {
	text = clean(text)
	if strings.TrimSpace(text) == "" {
		text = "unknown error"
	}
	body := ansi.Wrap(text, max(12, width-4), "")
	content := styleError.Render("error") + "\n" + styleText.Render(body)
	return card(colError, width, content)
}

func renderApproval(text string, width int) string {
	text = clean(text)
	inner := max(12, width-2)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i == 0 {
			lines[i] = styleWarn.Render(truncate(line, inner))
			continue
		}
		if strings.TrimSpace(line) == "" {
			lines[i] = ""
			continue
		}
		trimmed := strings.TrimLeft(line, " ")
		indent := line[:len(line)-len(trimmed)]
		textW := max(6, inner-len(indent))
		switch {
		case strings.HasPrefix(trimmed, "y allow"):
			lines[i] = styleDim.Render(truncate(line, inner))
		case strings.HasPrefix(trimmed, "+"):
			lines[i] = indent + styleDiffAdd.Render(truncate(trimmed, textW))
		case strings.HasPrefix(trimmed, "-"):
			lines[i] = indent + styleDiffDel.Render(truncate(trimmed, textW))
		case strings.HasPrefix(trimmed, "@"):
			lines[i] = indent + styleDiffHunk.Render(truncate(trimmed, textW))
		default:
			lines[i] = styleText.Render(truncate(line, inner))
		}
	}
	return card(colWarn, width, strings.Join(lines, "\n"))
}

func renderSystem(text string, width int) string {
	text = clean(text)
	body := ansi.Wrap(text, max(12, width-2), "")
	lines := strings.Split(body, "\n")
	for i := range lines {
		lines[i] = "  " + styleDim.Render(lines[i])
	}
	return strings.Join(lines, "\n")
}

// renderWelcome renders the startup screen: the wordmark, a tagline, and a
// compact workspace summary. Short terminals get the mark and a hint only so
// the logo never scrolls out of view.
func renderWelcome(opts Options, width, height int) string {
	inner := max(20, width-2)
	logo := renderLogo(inner)
	tagline := centerLines([]string{
		styleDim.Render("metis · local-first coding agent"),
	}, inner)
	hint := styleDim.Render("Type a task and press Enter") +
		styleDim.Render("  ·  / for commands  ·  tab switches mode")

	if height < 10 {
		return styleBrand.Render("METIS") + styleDim.Render(" · ") + styleLogoAgent.Render("AGENT") +
			"\n" + tagline + "\n  " + truncate(hint, max(10, inner-2))
	}
	if height < 17 {
		return logo + "\n" + tagline + "\n\n  " + truncate(hint, max(10, inner-2))
	}

	tools := "none"
	switch {
	case len(opts.Tools) > 6:
		tools = fmt.Sprintf("%d tools", len(opts.Tools))
	case len(opts.Tools) > 0:
		tools = strings.Join(opts.Tools, " · ")
	}
	approval := "ask"
	if opts.AutoApprove {
		approval = "auto"
	}
	thinking := "none"
	if opts.Thinking != "" {
		thinking = opts.Thinking
	}
	provider := opts.Provider
	if provider == "" {
		provider = "auto"
	}
	server := strings.TrimPrefix(strings.TrimPrefix(opts.Server, "https://"), "http://")
	session := opts.SessionID
	if session == "" {
		session = "ephemeral"
	}

	var b strings.Builder
	b.WriteString(logo)
	b.WriteString("\n")
	b.WriteString(tagline)
	b.WriteString("\n\n  ")
	b.WriteString(styleBrand.Render("workspace"))
	b.WriteString("\n  ")
	ruleW := inner - 2
	if ruleW > 60 {
		ruleW = 60
	}
	b.WriteString(styleRule.Render(strings.Repeat("─", max(10, ruleW))))
	b.WriteString("\n")

	writeRow := func(left, right [2]string, colW int) {
		cell := func(label, value string) string {
			vw := max(6, colW-9)
			text := styleDim.Render(fmt.Sprintf("%-8s", label)) + " " +
				styleText.Render(truncate(value, vw))
			return padRightANSI(text, colW)
		}
		b.WriteString("  " + cell(left[0], left[1]))
		if right[0] != "" {
			b.WriteString("  " + cell(right[0], right[1]))
		}
		b.WriteString("\n")
	}

	if width >= 68 {
		colW := max(20, (inner-4)/2)
		writeRow([2]string{"model", opts.Model}, [2]string{"approval", approval}, colW)
		writeRow([2]string{"provider", provider}, [2]string{"thinking", thinking}, colW)
		writeRow([2]string{"server", server}, [2]string{"tools", tools}, colW)
		writeRow([2]string{"session", session}, [2]string{"project", projectLabel(opts.Dir)}, colW)
	} else {
		writeRow([2]string{"model", opts.Model}, [2]string{}, max(12, inner-2))
		writeRow([2]string{"server", server}, [2]string{}, max(12, inner-2))
		writeRow([2]string{"approval", approval}, [2]string{}, max(12, inner-2))
	}

	b.WriteString("\n  ")
	b.WriteString(truncate(hint, max(10, inner-2)))
	return b.String()
}

func previewLines(s string, n int) []string {
	raw := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, n)
	for _, l := range raw {
		if len(out) == n {
			break
		}
		l = strings.TrimRight(l, " \t")
		if len(out) == 0 && strings.TrimSpace(l) == "" {
			continue
		}
		if strings.TrimSpace(l) == "" && strings.TrimSpace(out[len(out)-1]) == "" {
			continue
		}
		out = append(out, l)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func truncate(s string, width int) string {
	return ansi.Truncate(s, width, "…")
}

// oneLine collapses whitespace so a multi-line message fits in a status bar.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clean removes ANSI escapes and control characters so tool output or model
// text cannot break the terminal layout. Tabs are expanded and CRLF is
// normalised to LF.
func clean(s string) string {
	s = ansi.Strip(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == '\t':
			b.WriteString("    ")
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			// drop other control characters
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
