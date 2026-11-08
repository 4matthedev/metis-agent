package tools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

const (
	// maxDiffLines and maxDiffBytes keep an edit's diff from flooding the
	// model context or the transcript.
	maxDiffLines = 120
	maxDiffBytes = 16 * 1024
)

const (
	maxReadBytes   = 512 * 1024
	maxEditBytes   = 8 * 1024 * 1024
	maxSearchBytes = 2 * 1024 * 1024
)

// skipDirs are noisy directories that almost never contain user code worth
// exploring by default.
var skipDirs = map[string]bool{
	".git":          true,
	".hg":           true,
	".svn":          true,
	"node_modules":  true,
	".venv":         true,
	"venv":          true,
	"__pycache__":   true,
	".mypy_cache":   true,
	".pytest_cache": true,
	".idea":         true,
	".vscode":       true,
}

func NewFileReadTool(workDir string) *Tool {
	return &Tool{
		Name: "file_read",
		Description: "Read a text file from the project. Use start_line/end_line (1-based, inclusive) " +
			"for large files. Returns the raw file content without line numbers.",
		Parameters: objectSchema(map[string]any{
			"path":       propSchema{Type: "string", Description: "File path relative to the project, or absolute."},
			"start_line": propSchema{Type: "integer", Description: "Optional first line to return (1-based)."},
			"end_line":   propSchema{Type: "integer", Description: "Optional last line to return (inclusive)."},
		}, "path"),
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			p, err := resolvePath(workDir, str(args["path"]))
			if err != nil {
				return "", err
			}
			info, err := os.Stat(p)
			if err != nil {
				return "", fmt.Errorf("read %s: %w", p, err)
			}
			if info.IsDir() {
				return "", fmt.Errorf("%s is a directory (use list_dir)", p)
			}
			if info.Size() > 32*1024*1024 {
				return "", fmt.Errorf("%s is too large (%s); use shell with sed/head", p, humanSize(info.Size()))
			}
			if isBinaryFile(p) {
				return "", fmt.Errorf("%s looks like a binary file", p)
			}

			start := intArg(args, "start_line", 0)
			end := intArg(args, "end_line", 0)
			if start > 0 || end > 0 {
				return readLineRange(p, start, end)
			}

			b, err := os.ReadFile(p)
			if err != nil {
				return "", fmt.Errorf("read %s: %w", p, err)
			}
			if len(b) > maxReadBytes {
				return truncateText(string(b), maxReadBytes) +
					fmt.Sprintf("\n(file is %s; use start_line/end_line to read a range)", humanSize(info.Size())), nil
			}
			return string(b), nil
		},
	}
}

func readLineRange(p string, start, end int) (string, error) {
	if start < 1 {
		start = 1
	}
	if end > 0 && end < start {
		return "", fmt.Errorf("end_line (%d) is before start_line (%d)", end, start)
	}
	f, err := os.Open(p)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", p, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var out strings.Builder
	lineNo := 0
	total := 0
	wrote := false
	for sc.Scan() {
		lineNo++
		total = lineNo
		if lineNo < start {
			continue
		}
		if end > 0 && lineNo > end {
			// Keep counting so the caller gets the real total line count.
			continue
		}
		if wrote {
			out.WriteByte('\n')
		}
		out.WriteString(sc.Text())
		wrote = true
		if out.Len() > maxReadBytes {
			return out.String() + "\n... [range output truncated]", nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read %s: %w", p, err)
	}
	if !wrote {
		from, to := start, end
		if to == 0 {
			to = total
		}
		return fmt.Sprintf("(no lines in range %d-%d; file has %d lines)", from, to, total), nil
	}
	if end == 0 || end > total {
		out.WriteString(fmt.Sprintf("\n(file has %d lines)", total))
	}
	return out.String(), nil
}

func isBinaryFile(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	return bytes.IndexByte(buf[:n], 0) >= 0
}

func NewFileWriteTool(workDir string) *Tool {
	return &Tool{
		Name: "file_write",
		Description: "Create or overwrite a text file. Prefer file_edit for small changes to existing files. " +
			"Creates parent directories as needed.",
		Parameters: objectSchema(map[string]any{
			"path":    propSchema{Type: "string", Description: "File path relative to the project, or absolute."},
			"content": propSchema{Type: "string", Description: "Full file content."},
		}, "path", "content"),
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			p, err := resolvePath(workDir, str(args["path"]))
			if err != nil {
				return "", err
			}
			if !hasArg(args, "content") {
				return "", fmt.Errorf("content is required")
			}
			content := str(args["content"])
			mode := fs.FileMode(0o644)
			existed := false
			before := ""
			if info, err := os.Stat(p); err == nil {
				if info.IsDir() {
					return "", fmt.Errorf("%s is a directory", p)
				}
				mode = info.Mode().Perm()
				if info.Size() <= maxEditBytes {
					if b, err := os.ReadFile(p); err == nil && bytes.IndexByte(b, 0) < 0 {
						before = string(b)
						existed = true
					}
				}
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return "", fmt.Errorf("create directory: %w", err)
			}
			if err := os.WriteFile(p, []byte(content), mode); err != nil {
				return "", fmt.Errorf("write %s: %w", p, err)
			}
			msg := fmt.Sprintf("ok: wrote %d bytes to %s", len(content), relPath(workDir, p))
			if existed && before != content {
				msg += "\n" + unifiedDiff(before, content, relPath(workDir, p))
			}
			return msg, nil
		},
	}
}

func NewFileEditTool(workDir string) *Tool {
	return &Tool{
		Name: "file_edit",
		Description: "Make a precise edit by replacing old_text with new_text in an existing file. " +
			"old_text must match exactly (including indentation). If it occurs more than once, " +
			"include more surrounding context or set replace_all=true.",
		Parameters: objectSchema(map[string]any{
			"path":        propSchema{Type: "string", Description: "File path relative to the project, or absolute."},
			"old_text":    propSchema{Type: "string", Description: "Exact text to replace."},
			"new_text":    propSchema{Type: "string", Description: "Replacement text (may be empty)."},
			"replace_all": propSchema{Type: "boolean", Description: "Replace every occurrence instead of exactly one."},
		}, "path", "old_text"),
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			p, err := resolvePath(workDir, str(args["path"]))
			if err != nil {
				return "", err
			}
			oldText := str(args["old_text"])
			if oldText == "" {
				return "", fmt.Errorf("old_text is required")
			}
			newText := str(args["new_text"])
			replaceAll := boolArg(args, "replace_all")

			info, err := os.Stat(p)
			if err != nil {
				return "", fmt.Errorf("edit %s: %w", p, err)
			}
			if info.IsDir() {
				return "", fmt.Errorf("%s is a directory", p)
			}
			if info.Size() > maxEditBytes {
				return "", fmt.Errorf("%s is too large to edit (%s)", p, humanSize(info.Size()))
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return "", fmt.Errorf("edit %s: %w", p, err)
			}
			if bytes.IndexByte(b, 0) >= 0 {
				return "", fmt.Errorf("%s looks like a binary file", p)
			}
			content := string(b)
			count := strings.Count(content, oldText)
			switch {
			case count == 0:
				return "", fmt.Errorf("old_text was not found in %s", relPath(workDir, p))
			case count > 1 && !replaceAll:
				return "", fmt.Errorf("old_text appears %d times in %s; include more context or set replace_all=true",
					count, relPath(workDir, p))
			}
			if replaceAll {
				content = strings.ReplaceAll(content, oldText, newText)
			} else {
				content = strings.Replace(content, oldText, newText, 1)
			}
			if err := os.WriteFile(p, []byte(content), info.Mode().Perm()); err != nil {
				return "", fmt.Errorf("write %s: %w", p, err)
			}
			msg := fmt.Sprintf("ok: edited %s (%d replacement(s))", relPath(workDir, p), count)
			if d := unifiedDiff(string(b), content, relPath(workDir, p)); d != "" {
				msg += "\n" + d
			}
			return msg, nil
		},
	}
}

func NewListDirTool(workDir string) *Tool {
	return &Tool{
		Name: "list_dir",
		Description: "List files and directories in the project or elsewhere. Use depth to explore several levels. " +
			"Noisy directories such as .git and node_modules are skipped.",
		Parameters: objectSchema(map[string]any{
			"path":           propSchema{Type: "string", Description: "Directory relative to the project, or absolute (default .)."},
			"depth":          propSchema{Type: "integer", Description: "How many levels to list (default 2, max 6)."},
			"include_hidden": propSchema{Type: "boolean", Description: "Include dotfiles and dot-directories."},
			"max_entries":    propSchema{Type: "integer", Description: "Maximum entries to return (default 400, max 2000)."},
		}),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			root, err := resolvePath(workDir, defaultString(str(args["path"]), "."))
			if err != nil {
				return "", err
			}
			info, err := os.Stat(root)
			if err != nil {
				return "", fmt.Errorf("list %s: %w", root, err)
			}
			if !info.IsDir() {
				return "", fmt.Errorf("%s is not a directory", root)
			}
			depth := clamp(intArg(args, "depth", 2), 1, 6)
			maxEntries := clamp(intArg(args, "max_entries", 400), 1, 2000)
			includeHidden := boolArg(args, "include_hidden")

			var out []string
			walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if cerr := ctx.Err(); cerr != nil {
					return cerr
				}
				if p == root {
					return nil
				}
				rel, err := filepath.Rel(root, p)
				if err != nil {
					return nil
				}
				relSlash := filepath.ToSlash(rel)
				depthOf := strings.Count(relSlash, "/") + 1
				if d.IsDir() {
					if skipDirs[d.Name()] {
						return fs.SkipDir
					}
					if !includeHidden && strings.HasPrefix(d.Name(), ".") {
						return fs.SkipDir
					}
					if depthOf > depth {
						return fs.SkipDir
					}
					out = append(out, relSlash+"/")
				} else {
					if !includeHidden && strings.HasPrefix(d.Name(), ".") {
						return nil
					}
					if depthOf > depth {
						return nil
					}
					if d.Type()&fs.ModeSymlink != 0 {
						out = append(out, relSlash+" (symlink)")
					} else if fi, err := d.Info(); err == nil {
						out = append(out, fmt.Sprintf("%s (%s)", relSlash, humanSize(fi.Size())))
					} else {
						out = append(out, relSlash)
					}
				}
				if len(out) >= maxEntries {
					out = append(out, fmt.Sprintf("... (stopped after %d entries)", maxEntries))
					return fs.SkipAll
				}
				return nil
			})
			if walkErr != nil && walkErr != fs.SkipAll {
				return "", fmt.Errorf("list %s: %w", root, walkErr)
			}
			if len(out) == 0 {
				return "(empty directory)", nil
			}
			return strings.Join(out, "\n"), nil
		},
	}
}

func NewSearchTool(workDir string) *Tool {
	return &Tool{
		Name: "search",
		Description: "Search file contents using a regular expression. Returns path:line: text. " +
			"Use include to filter by glob (e.g. *.go or src/**/*.py).",
		Parameters: objectSchema(map[string]any{
			"pattern":     propSchema{Type: "string", Description: "Regular expression (literal text if invalid regex)."},
			"path":        propSchema{Type: "string", Description: "Directory relative to the project, or absolute (default .)."},
			"include":     propSchema{Type: "string", Description: "Optional glob filter, e.g. *.go."},
			"max_results": propSchema{Type: "integer", Description: "Maximum matches (default 100, max 500)."},
		}, "pattern"),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			patternArg := str(args["pattern"])
			if patternArg == "" {
				return "", fmt.Errorf("pattern is required")
			}
			re, err := regexp.Compile(patternArg)
			if err != nil {
				re = regexp.MustCompile(regexp.QuoteMeta(patternArg))
			}
			root, err := resolvePath(workDir, defaultString(str(args["path"]), "."))
			if err != nil {
				return "", err
			}
			info, err := os.Stat(root)
			if err != nil {
				return "", fmt.Errorf("search %s: %w", root, err)
			}
			if !info.IsDir() {
				return "", fmt.Errorf("%s is not a directory", root)
			}
			include := str(args["include"])
			maxResults := clamp(intArg(args, "max_results", 100), 1, 500)

			var results []string
			walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if cerr := ctx.Err(); cerr != nil {
					return cerr
				}
				if d.IsDir() {
					if p != root && skipDirs[d.Name()] {
						return fs.SkipDir
					}
					return nil
				}
				if !d.Type().IsRegular() {
					return nil
				}
				rel, err := filepath.Rel(root, p)
				if err != nil {
					return nil
				}
				relSlash := filepath.ToSlash(rel)
				if include != "" && !matchInclude(include, relSlash) {
					return nil
				}
				fi, err := d.Info()
				if err != nil || fi.Size() > maxSearchBytes {
					return nil
				}
				b, err := os.ReadFile(p)
				if err != nil || bytes.IndexByte(b, 0) >= 0 {
					return nil
				}
				for i, line := range strings.Split(string(b), "\n") {
					line = strings.TrimSuffix(line, "\r")
					if !re.MatchString(line) {
						continue
					}
					if len(line) > 300 {
						line = line[:300] + "…"
					}
					results = append(results, fmt.Sprintf("%s:%d: %s", relSlash, i+1, line))
					if len(results) >= maxResults {
						return fs.SkipAll
					}
				}
				return nil
			})
			if walkErr != nil && walkErr != fs.SkipAll {
				return "", fmt.Errorf("search %s: %w", root, walkErr)
			}
			if len(results) == 0 {
				return fmt.Sprintf("no matches for %q", patternArg), nil
			}
			return strings.Join(results, "\n"), nil
		},
	}
}

func matchInclude(pattern, rel string) bool {
	pattern = filepath.ToSlash(pattern)
	rel = filepath.ToSlash(rel)
	if idx := strings.Index(pattern, "**/"); idx >= 0 {
		prefix := pattern[:idx]
		suffix := pattern[idx+3:]
		if prefix != "" && !strings.HasPrefix(rel, prefix) {
			return false
		}
		ok, _ := path.Match(suffix, path.Base(rel))
		return ok
	}
	if strings.Contains(pattern, "/") {
		ok, _ := path.Match(pattern, rel)
		return ok
	}
	ok, _ := path.Match(pattern, path.Base(rel))
	return ok
}

func relPath(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(rel)
}

func defaultString(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// unifiedDiff returns a capped unified diff between before and after. Paths
// inside the project use the familiar a/ and b/ prefixes; outside paths are
// shown as absolute.
func unifiedDiff(before, after, rel string) string {
	if before == after {
		return ""
	}
	fromFile, toFile := "a/"+rel, "b/"+rel
	if filepath.IsAbs(filepath.FromSlash(rel)) {
		fromFile, toFile = rel, rel
	}
	d, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(before),
		B:        difflib.SplitLines(after),
		FromFile: fromFile,
		ToFile:   toFile,
		Context:  3,
	})
	if err != nil || strings.TrimSpace(d) == "" {
		return ""
	}
	return capDiff(d)
}

func capDiff(d string) string {
	d = strings.TrimRight(d, "\n")
	var b strings.Builder
	lines := strings.Split(d, "\n")
	for i, line := range lines {
		if i >= maxDiffLines || b.Len()+len(line)+1 > maxDiffBytes {
			b.WriteString("\n... (diff truncated)")
			break
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String()
}
