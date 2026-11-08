package tui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
)

type markdownKey struct {
	width int
	dark  bool
}

var (
	mdMu        sync.Mutex
	mdRenderers = map[markdownKey]*glamour.TermRenderer{}
)

func strPtr(s string) *string { return &s }
func uintPtr(u uint) *uint    { return &u }
func boolPtr(b bool) *bool    { return &b }

// markdownStyle is a restrained theme that follows the terminal background
// instead of imposing glamour's default document margins and heading blocks.
func markdownStyle(dark bool) ansi.StyleConfig {
	var cfg ansi.StyleConfig
	accent, muted, text := "#7AA2F7", "#565F89", "#C0CAF5"
	if dark {
		cfg = styles.DarkStyleConfig
	} else {
		cfg = styles.LightStyleConfig
		accent, muted, text = "#1D4ED8", "#6B7280", "#111827"
	}

	cfg.Document.Margin = uintPtr(0)
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = "\n"
	cfg.Document.Color = strPtr(text)

	cfg.Heading.Color = strPtr(accent)
	cfg.Heading.Bold = boolPtr(true)
	cfg.H1 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{
		Color: strPtr(accent), Bold: boolPtr(true), BlockSuffix: "\n",
	}}
	cfg.H2 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{
		Color: strPtr(accent), Bold: boolPtr(true), Prefix: "## ", BlockSuffix: "\n",
	}}
	cfg.H3 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{
		Color: strPtr(accent), Bold: boolPtr(true), Prefix: "### ", BlockSuffix: "\n",
	}}
	cfg.H4 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{
		Color: strPtr(accent), Bold: boolPtr(true), Prefix: "#### ",
	}}
	cfg.H5 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{
		Color: strPtr(accent), Bold: boolPtr(true), Prefix: "##### ",
	}}
	cfg.H6 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{
		Color: strPtr(muted), Prefix: "###### ",
	}}

	cfg.Strong.Bold = boolPtr(true)
	cfg.Emph.Italic = boolPtr(true)

	cfg.Code.Color = strPtr(accent)
	cfg.Code.BackgroundColor = nil
	cfg.CodeBlock.Margin = uintPtr(0)
	cfg.CodeBlock.Color = strPtr(text)

	cfg.Link.Color = strPtr(accent)
	cfg.Link.Underline = boolPtr(true)
	cfg.LinkText.Color = strPtr(accent)
	cfg.LinkText.Bold = boolPtr(false)

	cfg.HorizontalRule.Color = strPtr(muted)
	cfg.HorizontalRule.Format = "\n" + strings.Repeat("─", 24) + "\n"
	return cfg
}

func markdownRenderer(width int, dark bool) (*glamour.TermRenderer, error) {
	key := markdownKey{width: width, dark: dark}
	mdMu.Lock()
	defer mdMu.Unlock()
	if r, ok := mdRenderers[key]; ok {
		return r, nil
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(markdownStyle(dark)),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}
	mdRenderers[key] = r
	return r, nil
}

// renderMarkdown renders assistant turns as formatted Markdown. Streaming text
// stays plain; the finished block is re-rendered once the turn completes.
func renderMarkdown(text string, width int) string {
	width = max(12, width)
	r, err := markdownRenderer(width, darkBackground())
	if err != nil {
		return text
	}
	out, err := r.Render(text)
	if err != nil {
		return text
	}
	return trimRendered(out)
}

// trimRendered removes the padding glamour adds to fill the word-wrap width.
// Padding spaces can carry their own SGR sequences, so plain
// strings.TrimRight would miss them.
func trimRendered(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = trimLineRight(line)
	}
	return strings.Join(lines, "\n")
}

func trimLineRight(line string) string {
	var b strings.Builder
	lastVisible := 0
	for i := 0; i < len(line); {
		if line[i] == 0x1b {
			j := i + 1
			if j < len(line) && line[j] == '[' {
				j++
				for j < len(line) && (line[j] < '@' || line[j] > '~') {
					j++
				}
				if j < len(line) {
					j++
				}
			} else if j < len(line) {
				j++
			}
			b.WriteString(line[i:j])
			i = j
			continue
		}
		start := i
		for i < len(line) && line[i] != 0x1b {
			i++
		}
		chunk := line[start:i]
		b.WriteString(chunk)
		if n := len(strings.TrimRight(chunk, " \t")); n > 0 {
			lastVisible = b.Len() - (len(chunk) - n)
		}
	}
	if lastVisible == 0 {
		return ""
	}
	trimmed := b.String()[:lastVisible]
	if trimmed != b.String() {
		// The removed padding may have left an SGR style active.
		trimmed += "\x1b[0m"
	}
	return trimmed
}

// renderAssistant renders one completed assistant turn with the hanging label
// used by the rest of the transcript.
func renderAssistant(text string, width int, streaming bool) string {
	if streaming {
		return renderChat("metis", styleMetis, text, width, true)
	}
	body := renderMarkdown(text, max(12, width-7))
	if strings.TrimSpace(stripANSI(body)) == "" {
		body = " "
	}
	return prefixLabel("metis", styleMetis, body)
}

func prefixLabel(name string, label lipgloss.Style, body string) string {
	const prefixW = 7
	prefix := label.Render(fmt.Sprintf("%-5s", name)) + styleDim.Render("  ")
	indent := strings.Repeat(" ", prefixW)
	lines := strings.Split(body, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = prefix + lines[i]
		} else {
			lines[i] = indent + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}
