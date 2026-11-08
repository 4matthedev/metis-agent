// Package tools provides Metis's tool registry and approval-gated executors.
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"metis-agent/internal/llm"
)

// Handler executes one tool call. ctx is cancelled when the user interrupts
// a run; long-running tools should honour it.
type Handler func(ctx context.Context, args map[string]any) (string, error)

// Tool describes one callable tool with its OpenAI function schema.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Handler     Handler
}

// spec returns the OpenAI-compatible definition sent to the server.
func (t *Tool) spec() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		},
	}
}

func objectSchema(props map[string]any, required ...string) map[string]any {
	m := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

type propSchema struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func boolArg(args map[string]any, key string) bool {
	if b, ok := args[key].(bool); ok {
		return b
	}
	return false
}

func intArg(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return def
}

func hasArg(args map[string]any, key string) bool {
	_, ok := args[key]
	return ok
}

// stringList converts a JSON array argument into a string slice.
func stringList(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// clamp limits v to [lo, hi].
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// truncateText keeps the beginning and end of s when it is longer than max.
func truncateText(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	headLen := max * 2 / 3
	tailLen := max - headLen
	head := strings.ToValidUTF8(s[:headLen], "")
	tail := strings.ToValidUTF8(s[len(s)-tailLen:], "")
	return fmt.Sprintf("%s\n... [%d bytes truncated] ...\n%s", head, len(s)-max, tail)
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func NewTimeTool() *Tool {
	return &Tool{
		Name:        "time",
		Description: "Get the current local date and time in ISO 8601 format.",
		Parameters:  objectSchema(map[string]any{}),
		Handler: func(context.Context, map[string]any) (string, error) {
			return time.Now().Format(time.RFC3339), nil
		},
	}
}

// resolvePath turns a user-supplied path into an absolute path. Relative paths
// are resolved against root, but absolute paths, "..", and symlinks are
// allowed: Metis no longer confines file tools to a sandbox. The approval
// layer asks the user before touching anything outside the project.
func resolvePath(root, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	if p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return home, nil
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		p = filepath.Join(home, p[2:])
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve project directory %q: %w", root, err)
	}
	abs, err := filepath.Abs(filepath.Join(base, p))
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", p, err)
	}
	return abs, nil
}

// safePath resolves p under root, rejecting absolute paths, ".." and anything
// that escapes root (including via symlinks). It remains available for callers
// that want an explicitly sandboxed resolver.
func safePath(root, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("absolute path %q is not allowed inside the sandbox", p)
	}
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == ".." {
			return "", fmt.Errorf("path %q contains '..' and is not allowed inside the sandbox", p)
		}
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve sandbox %q: %w", root, err)
	}
	rootAbs = resolveSymlinks(rootAbs)

	full := filepath.Join(rootAbs, p)
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", p, err)
	}
	// Resolve the deepest existing ancestor so symlinks cannot be used to
	// escape the sandbox, even when the target does not exist yet.
	abs = resolveSymlinks(abs)

	sep := string(filepath.Separator)
	if abs != rootAbs && !strings.HasPrefix(abs, rootAbs+sep) {
		return "", fmt.Errorf("path %q escapes the sandbox at %q", p, rootAbs)
	}
	return abs, nil
}

// resolveSymlinks resolves symlinks for the deepest existing ancestor of p
// and rejoins the non-existent remainder.
func resolveSymlinks(p string) string {
	rest := ""
	cur := p
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			if rest == "" {
				return resolved
			}
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
