package tui

import (
	"bytes"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	darkOnce      sync.Once
	darkBG        bool
	themeOverride string
)

// SetTheme forces the dark or light palette. "auto" (or any other value)
// restores terminal detection. Call it before starting the interface.
func SetTheme(mode string) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "dark":
		themeOverride = "dark"
	case "light":
		themeOverride = "light"
	default:
		themeOverride = ""
	}
	darkOnce = sync.Once{}
}

// darkBackground reports whether the terminal has a dark background. The
// answer is cached so the terminal is only queried once.
func darkBackground() bool {
	if themeOverride != "" {
		return themeOverride == "dark"
	}
	darkOnce.Do(func() {
		darkBG = lipgloss.HasDarkBackground()
	})
	return darkBG
}

// highlightCode syntax-highlights code using Chroma's 256-colour formatter.
// The filename selects the lexer; when it is unknown the content is analysed.
// It returns false when no reasonable lexer is available.
func highlightCode(code, filename string) (string, bool) {
	if strings.TrimSpace(code) == "" {
		return "", false
	}
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		return "", false
	}
	lexer = chroma.Coalesce(lexer)

	styleName := "github-dark"
	if !darkBackground() {
		styleName = "github"
	}
	formatter := formatters.Get("terminal256")

	var buf bytes.Buffer
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return "", false
	}
	if err := formatter.Format(&buf, styles.Get(styleName), it); err != nil {
		return "", false
	}
	out := strings.TrimRight(buf.String(), "\n")
	if strings.TrimSpace(stripANSI(out)) == "" {
		return "", false
	}
	return out, true
}

// highlightPreview highlights only the preview lines so very large tool output
// never gets tokenised in full.
func highlightPreview(lines []string, filename string) ([]string, bool) {
	if len(lines) == 0 || filename == "" {
		return nil, false
	}
	out, ok := highlightCode(strings.Join(lines, "\n"), filename)
	if !ok {
		return nil, false
	}
	return strings.Split(strings.TrimRight(out, "\n"), "\n"), true
}

func stripANSI(s string) string {
	return ansi.Strip(s)
}
