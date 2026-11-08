// Package instructions loads project and global guidance files that are
// appended to the system prompt.
package instructions

import (
	"os"
	"path/filepath"
	"strings"

	"metis-agent/internal/config"
)

const (
	maxDocBytes   = 32 * 1024
	maxTotalBytes = 96 * 1024
)

// Document is one loaded instruction file.
type Document struct {
	Path    string
	Content string
}

// candidates returns global and project instruction paths in load order.
func candidates(workDir string, extra []string) []string {
	var out []string
	if dir, err := config.ConfigDir(); err == nil {
		for _, name := range []string{"AGENTS.md", "METIS.md", "rules.md"} {
			out = append(out, filepath.Join(dir, name))
		}
		out = append(out, filepath.Join(dir, ".metis-agent", "rules.md"))
	}
	for _, name := range []string{
		"AGENTS.md",
		"METIS.md",
		"CLAUDE.md",
		filepath.Join(".metis-agent", "rules.md"),
		filepath.Join(".metis-agent", "instructions.md"),
	} {
		out = append(out, filepath.Join(workDir, name))
	}
	for _, p := range extra {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, p)
		}
		out = append(out, p)
	}
	return out
}

// Load reads every instruction file that exists. Unreadable files are skipped;
// the caller can use the returned documents to report what was loaded.
func Load(workDir string, extra []string) []Document {
	seen := make(map[string]bool)
	var docs []Document
	total := 0
	for _, p := range candidates(workDir, extra) {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		if len(b) > maxDocBytes {
			b = b[:maxDocBytes]
		}
		if total+len(b) > maxTotalBytes {
			break
		}
		text := strings.TrimSpace(string(b))
		if text == "" {
			continue
		}
		total += len(b)
		docs = append(docs, Document{Path: abs, Content: text})
	}
	return docs
}

// Prompt renders documents as a system-prompt section. Empty input returns an
// empty string so callers can append unconditionally.
func Prompt(docs []Document) string {
	if len(docs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nInstructions from the user (follow these in addition to the rules above):\n")
	for _, d := range docs {
		b.WriteString("\n--- ")
		b.WriteString(d.Path)
		b.WriteString(" ---\n")
		b.WriteString(d.Content)
		if !strings.HasSuffix(d.Content, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}
