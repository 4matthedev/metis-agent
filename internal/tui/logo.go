package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The Metis wordmark. Each line is padded at render time so both halves stay
// aligned even though the AGENT glyphs have slightly different natural widths.
var logoMetis = []string{
	"███╗   ███╗███████╗████████╗██╗███████╗",
	"████╗ ████║██╔════╝╚══██╔══╝██║██╔════╝",
	"██╔████╔██║█████╗     ██║   ██║███████╗",
	"██║╚██╔╝██║██╔══╝     ██║   ██║╚════██║",
	"██║ ╚═╝ ██║███████╗   ██║   ██║███████║",
	"╚═╝     ╚═╝╚══════╝   ╚═╝   ╚═╝╚══════╝",
}

var logoAgent = []string{
	"█████╗  ██████╗ ███████╗███╗   ██╗████████╗",
	"██╔══██╗██╔════╝ ██╔════╝████╗  ██║╚══██╔══╝",
	"███████║██║  ███╗█████╗  ██╔██╗ ██║   ██║",
	"██╔══██║██║   ██║██╔══╝  ██║╚██╗██║   ██║",
	"██║  ██║╚██████╔╝███████╗██║ ╚████║   ██║",
	"╚═╝  ╚═╝ ╚═════╝ ╚══════╝╚═╝  ╚═══╝   ╚═╝",
}

const (
	logoMetisW = 39
	logoAgentW = 44
	logoGapW   = 4
	logoFullW  = logoMetisW + logoGapW + logoAgentW
)

// renderLogo draws the Metis wordmark using the room it is given. Wide
// terminals get the full lockup, mid-sized ones get the METIS mark plus a
// spaced AGENT word, and narrow ones fall back to a single-line wordmark.
func renderLogo(width int) string {
	if width >= logoFullW {
		lines := make([]string, len(logoMetis))
		for i := range logoMetis {
			left := styleBrand.Render(padRight(logoMetis[i], logoMetisW))
			right := styleLogoAgent.Render(padRight(logoAgent[i], logoAgentW))
			lines[i] = left + strings.Repeat(" ", logoGapW) + right
		}
		return centerLines(lines, width)
	}
	if width >= logoMetisW {
		lines := make([]string, 0, len(logoMetis)+1)
		for _, line := range logoMetis {
			lines = append(lines, styleBrand.Render(line))
		}
		lines = append(lines, styleLogoAgent.Render("A G E N T"))
		return centerLines(lines, width)
	}
	return centerLines([]string{
		styleBrand.Render("METIS") + styleDim.Render(" · ") + styleLogoAgent.Render("AGENT"),
	}, width)
}

func centerLines(lines []string, width int) string {
	for i, line := range lines {
		pad := (width - lipgloss.Width(line)) / 2
		if pad > 0 {
			lines[i] = strings.Repeat(" ", pad) + line
		}
	}
	return strings.Join(lines, "\n")
}

func padRight(s string, width int) string {
	if pad := width - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// padRightANSI pads a styled line to a display width without disturbing the
// escape sequences it carries.
func padRightANSI(s string, width int) string {
	if pad := width - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}
