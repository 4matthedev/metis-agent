package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Palette. Adaptive colors keep the interface readable on both dark and light
// terminals while staying understated.
var (
	colAccent = lipgloss.AdaptiveColor{Light: "#1D4ED8", Dark: "#7AA2F7"}
	colUser   = lipgloss.AdaptiveColor{Light: "#047857", Dark: "#9ECE6A"}
	colTool   = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#E0AF68"}
	colOK     = lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#9ECE6A"}
	colError  = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F7768E"}
	colMuted  = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#565F89"}
	colRule   = lipgloss.AdaptiveColor{Light: "#D1D5DB", Dark: "#3B4261"}
)

var (
	styleText      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#111827", Dark: "#C0CAF5"})
	styleBrand     = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleHeaderSub = lipgloss.NewStyle().Foreground(colMuted)
	styleRule      = lipgloss.NewStyle().Foreground(colRule)
	styleUser      = lipgloss.NewStyle().Foreground(colUser).Bold(true)
	styleMetis     = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleTool      = lipgloss.NewStyle().Foreground(colTool).Bold(true)
	styleOK        = lipgloss.NewStyle().Foreground(colOK)
	styleError     = lipgloss.NewStyle().Foreground(colError).Bold(true)
	styleDim       = lipgloss.NewStyle().Foreground(colMuted)
	styleCursor    = lipgloss.NewStyle().Foreground(colAccent)
	styleStatus    = lipgloss.NewStyle().Foreground(colMuted)
	styleStatusOn  = lipgloss.NewStyle().Foreground(colAccent)
	styleComposer  = lipgloss.NewStyle().Padding(0, 1)
	styleFooter    = lipgloss.NewStyle().Foreground(colMuted)
	styleSuggest   = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#1A1B26"}).
			Background(colAccent)

	styleDiffAdd     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#9ECE6A"})
	styleDiffDel     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F7768E"})
	styleDiffHunk    = lipgloss.NewStyle().Foreground(colAccent)
	styleDiffMeta    = lipgloss.NewStyle().Foreground(colMuted).Bold(true)
	styleDiffContext = lipgloss.NewStyle().Foreground(colMuted)
)

// renderChat renders a turn with aligned labels and a hanging indent:
//
//	you    add a test for add()
//	metis  I'll inspect the project first.
//	       ...
func renderChat(name string, label lipgloss.Style, text string, width int, streaming bool) string {
	text = clean(text)
	const prefixW = 7
	prefix := label.Render(fmt.Sprintf("%-5s", name)) + styleDim.Render("  ")
	bodyW := width - prefixW
	if bodyW < 12 {
		bodyW = 12
	}
	if streaming {
		bodyW-- // leave room for the cursor
	}
	if text == "" {
		text = " "
	}
	body := ansi.Wrap(text, bodyW, "")
	lines := strings.Split(body, "\n")
	if streaming {
		lines[len(lines)-1] += styleCursor.Render("▌")
	}
	indent := strings.Repeat(" ", prefixW)

	var out strings.Builder
	for i, line := range lines {
		if i == 0 {
			out.WriteString(prefix)
		} else {
			out.WriteString(indent)
		}
		out.WriteString(styleText.Render(line))
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// renderTool renders a tool call as a compact log entry:
//
//	shell      $ go test ./...
//	ok         exit code: 0  ·  1.2s
//	           PASS
func renderTool(tool, args, result, duration string, running bool, width, maxLines int, highlight bool) string {
	const (
		indent  = 2
		nameW   = 16
		textCol = indent + nameW + 1
		statusW = nameW
	)
	tool = clean(tool)
	args = clean(args)
	result = clean(result)

	name := truncate(tool, nameW)
	head := strings.Repeat(" ", indent) + styleTool.Render(fmt.Sprintf("%-*s", nameW, name))
	if hint := formatToolArgs(tool, args); hint != "" {
		hintW := max(10, width-textCol)
		head += " " + styleDim.Render(truncate(hint, hintW))
	}

	failed := toolFailed(result)
	status := "ok"
	statusStyle := styleOK
	if running {
		status = "running"
		statusStyle = styleStatusOn
	} else if failed {
		status = "failed"
		statusStyle = styleError
	}
	statusCell := statusStyle.Render(fmt.Sprintf("%-*s", statusW, status))

	lines := []string{head}
	if running && strings.TrimSpace(result) == "" {
		lines = append(lines, strings.Repeat(" ", indent)+statusCell+" "+styleDim.Render("working…"))
		return strings.Join(lines, "\n")
	}
	if strings.TrimSpace(result) == "" {
		return strings.Join(lines, "\n")
	}

	preview := previewLines(result, maxLines)
	isDiff := looksLikeDiff(result)
	textW := max(10, width-textCol)

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
		default:
			text = styleText.Render(truncate(line, textW))
		}
		if i == 0 {
			row := strings.Repeat(" ", indent) + statusCell + " "
			if failed {
				row += styleError.Render(truncate(line, textW))
			} else {
				row += text
			}
			if duration != "" {
				row += styleDim.Render("  ·  " + duration)
			}
			lines = append(lines, row)
			continue
		}
		if highlightedOK && i < len(highlighted) {
			lines = append(lines, strings.Repeat(" ", textCol)+text)
			continue
		}
		if isDiff {
			lines = append(lines, strings.Repeat(" ", textCol)+text)
			continue
		}
		lines = append(lines, strings.Repeat(" ", textCol)+styleDim.Render(truncate(line, textW)))
	}
	return strings.Join(lines, "\n")
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
	const prefixW = 7
	prefix := styleError.Render(fmt.Sprintf("%-5s", "error")) + styleDim.Render("  ")
	bodyW := max(12, width-prefixW)
	body := ansi.Wrap(text, bodyW, "")
	lines := strings.Split(body, "\n")
	indent := strings.Repeat(" ", prefixW)
	for i := range lines {
		if i == 0 {
			lines[i] = prefix + lines[i]
		} else {
			lines[i] = indent + lines[i]
		}
	}
	return strings.Join(lines, "\n")
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

// renderWelcome renders the compact startup panel.
func renderWelcome(opts Options, width int) string {
	inner := max(20, width-4)
	ruleW := inner
	if ruleW > 60 {
		ruleW = 60
	}

	var b strings.Builder
	b.WriteString("  " + styleBrand.Render("workspace") + "\n")
	b.WriteString("  " + styleRule.Render(strings.Repeat("─", ruleW)) + "\n")

	tools := "none"
	switch {
	case len(opts.Tools) > 6:
		tools = fmt.Sprintf("%d tools · %s · …", len(opts.Tools), strings.Join(opts.Tools[:5], " · "))
	case len(opts.Tools) > 0:
		tools = strings.Join(opts.Tools, " · ")
	}
	rows := [][2]string{}
	if opts.Provider != "" {
		rows = append(rows, [2]string{"provider", opts.Provider})
	}
	approval := "ask"
	if opts.AutoApprove {
		approval = "auto"
	}
	rows = append(rows,
		[2]string{"model", opts.Model},
		[2]string{"server", strings.TrimPrefix(strings.TrimPrefix(opts.Server, "https://"), "http://")},
		[2]string{"project", opts.Dir},
		[2]string{"approval", approval},
		[2]string{"tools", tools},
	)
	if opts.SessionID != "" {
		rows = append(rows, [2]string{"session", opts.SessionID})
	}
	valueW := max(10, inner-10)
	for _, row := range rows {
		b.WriteString("  " + styleDim.Render(fmt.Sprintf("%-8s", row[0])) + " " +
			styleText.Render(truncate(row[1], valueW)) + "\n")
	}
	b.WriteString("\n  " + styleText.Render("Type a task and press Enter.") +
		styleDim.Render("  /help for commands."))
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
