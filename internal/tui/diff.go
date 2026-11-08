package tui

import "strings"

// looksLikeDiff reports whether a tool result contains a unified diff. This is
// deliberately strict so ordinary output that starts with + or - is not
// mis-coloured.
func looksLikeDiff(s string) bool {
	return strings.Contains(s, "\n--- a/") &&
		strings.Contains(s, "\n+++ b/") &&
		strings.Contains(s, "\n@@")
}

// diffLineKind classifies one line of a unified diff.
type diffLineKind int

const (
	diffNone diffLineKind = iota
	diffMeta
	diffHunk
	diffAdd
	diffDel
	diffContext
)

func classifyDiffLine(line string) diffLineKind {
	switch {
	case strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ "):
		return diffMeta
	case strings.HasPrefix(line, "@@"):
		return diffHunk
	case strings.HasPrefix(line, "+"):
		return diffAdd
	case strings.HasPrefix(line, "-"):
		return diffDel
	case strings.HasPrefix(line, " "):
		return diffContext
	}
	return diffNone
}

// renderDiffLine applies diff colours to one line. The line must already be
// cleaned of raw ANSI sequences.
func renderDiffLine(line string, width int) string {
	line = truncate(line, width)
	switch classifyDiffLine(line) {
	case diffMeta:
		return styleDiffMeta.Render(line)
	case diffHunk:
		return styleDiffHunk.Render(line)
	case diffAdd:
		return styleDiffAdd.Render(line)
	case diffDel:
		return styleDiffDel.Render(line)
	case diffContext:
		return styleDiffContext.Render(line)
	default:
		return styleText.Render(line)
	}
}
