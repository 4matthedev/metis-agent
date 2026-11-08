package tools

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pmezard/go-difflib/difflib"
)

const maxCopyBytes = 64 * 1024 * 1024

// NewGlobTool finds files by glob pattern.
func NewGlobTool(workDir string) *Tool {
	return &Tool{
		Name: "glob",
		Description: "Find files by glob pattern. Supports * and ** (for example **/*.go or src/**/*.ts). " +
			"Returns paths relative to the project, or absolute. Noisy directories such as .git and node_modules are skipped.",
		Parameters: objectSchema(map[string]any{
			"pattern":        propSchema{Type: "string", Description: "Glob pattern, e.g. **/*.go."},
			"path":           propSchema{Type: "string", Description: "Directory relative to the project, or absolute (default .)."},
			"include_hidden": propSchema{Type: "boolean", Description: "Include dotfiles and dot-directories."},
			"max_results":    propSchema{Type: "integer", Description: "Maximum matches (default 200, max 2000)."},
		}, "pattern"),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			pattern := strings.TrimSpace(str(args["pattern"]))
			if pattern == "" {
				return "", fmt.Errorf("pattern is required")
			}
			root, err := resolvePath(workDir, defaultString(str(args["path"]), "."))
			if err != nil {
				return "", err
			}
			info, err := os.Stat(root)
			if err != nil {
				return "", fmt.Errorf("glob %s: %w", root, err)
			}
			if !info.IsDir() {
				return "", fmt.Errorf("%s is not a directory", root)
			}
			includeHidden := boolArg(args, "include_hidden")
			maxResults := clamp(intArg(args, "max_results", 200), 1, 2000)

			var matches []string
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
				if d.IsDir() {
					if skipDirs[d.Name()] {
						return fs.SkipDir
					}
					if !includeHidden && strings.HasPrefix(d.Name(), ".") {
						return fs.SkipDir
					}
					return nil
				}
				if !includeHidden && strings.HasPrefix(d.Name(), ".") {
					return nil
				}
				if !matchInclude(pattern, relSlash) {
					return nil
				}
				matches = append(matches, relSlash)
				if len(matches) >= maxResults {
					return fs.SkipAll
				}
				return nil
			})
			if walkErr != nil && walkErr != fs.SkipAll {
				return "", fmt.Errorf("glob %s: %w", root, walkErr)
			}
			if len(matches) == 0 {
				return fmt.Sprintf("no files match %q", pattern), nil
			}
			out := strings.Join(matches, "\n")
			if len(matches) >= maxResults {
				out += fmt.Sprintf("\n... (stopped after %d matches)", maxResults)
			}
			return out, nil
		},
	}
}

// NewFileStatTool reports metadata for a file or directory.
func NewFileStatTool(workDir string) *Tool {
	return &Tool{
		Name:        "file_stat",
		Description: "Show metadata for a file or directory: type, size, permissions, modification time, and line count for text files.",
		Parameters: objectSchema(map[string]any{
			"path": propSchema{Type: "string", Description: "Path relative to the project, or absolute."},
		}, "path"),
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			p, err := resolvePath(workDir, str(args["path"]))
			if err != nil {
				return "", err
			}
			info, err := os.Lstat(p)
			if err != nil {
				return "", fmt.Errorf("stat %s: %w", p, err)
			}
			kind := "file"
			switch {
			case info.IsDir():
				kind = "directory"
			case info.Mode()&os.ModeSymlink != 0:
				kind = "symlink"
			case !info.Mode().IsRegular():
				kind = info.Mode().Type().String()
			}
			var b strings.Builder
			fmt.Fprintf(&b, "path: %s\n", relPath(workDir, p))
			fmt.Fprintf(&b, "type: %s\n", kind)
			fmt.Fprintf(&b, "size: %s (%d bytes)\n", humanSize(info.Size()), info.Size())
			fmt.Fprintf(&b, "mode: %s\n", info.Mode().Perm())
			fmt.Fprintf(&b, "modified: %s\n", info.ModTime().Format(time.RFC3339))
			if kind == "symlink" {
				if target, err := os.Readlink(p); err == nil {
					fmt.Fprintf(&b, "target: %s\n", target)
				}
			}
			if kind == "file" && info.Size() <= 2*1024*1024 {
				if lines, ok := countTextLines(p); ok {
					fmt.Fprintf(&b, "lines: %d\n", lines)
				}
			}
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}

func countTextLines(p string) (int, bool) {
	f, err := os.Open(p)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	lines := 0
	for sc.Scan() {
		if bytes.IndexByte(sc.Bytes(), 0) >= 0 {
			return 0, false
		}
		lines++
	}
	return lines, sc.Err() == nil
}

// NewMakeDirTool creates a directory tree.
func NewMakeDirTool(workDir string) *Tool {
	return &Tool{
		Name:        "make_dir",
		Description: "Create a directory (including parent directories). Succeeds if the directory already exists.",
		Parameters: objectSchema(map[string]any{
			"path": propSchema{Type: "string", Description: "Directory path relative to the project, or absolute."},
		}, "path"),
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			p, err := resolvePath(workDir, str(args["path"]))
			if err != nil {
				return "", err
			}
			if info, err := os.Stat(p); err == nil {
				if !info.IsDir() {
					return "", fmt.Errorf("%s already exists and is not a directory", relPath(workDir, p))
				}
				return fmt.Sprintf("ok: directory already exists: %s", relPath(workDir, p)), nil
			}
			if err := os.MkdirAll(p, 0o755); err != nil {
				return "", fmt.Errorf("create directory %s: %w", relPath(workDir, p), err)
			}
			return fmt.Sprintf("ok: created directory %s", relPath(workDir, p)), nil
		},
	}
}

// NewFileMoveTool moves or renames a file or directory.
func NewFileMoveTool(workDir string) *Tool {
	return &Tool{
		Name:        "file_move",
		Description: "Move or rename a file or directory in the project or elsewhere. Creates the destination parent directory. Set overwrite=true to replace an existing file.",
		Parameters: objectSchema(map[string]any{
			"from":      propSchema{Type: "string", Description: "Source path relative to the project, or absolute."},
			"to":        propSchema{Type: "string", Description: "Destination path relative to the project, or absolute."},
			"overwrite": propSchema{Type: "boolean", Description: "Replace the destination if it exists."},
		}, "from", "to"),
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			from, err := resolvePath(workDir, str(args["from"]))
			if err != nil {
				return "", fmt.Errorf("source: %w", err)
			}
			to, err := resolvePath(workDir, str(args["to"]))
			if err != nil {
				return "", fmt.Errorf("destination: %w", err)
			}
			if isSandboxRoot(workDir, from) {
				return "", fmt.Errorf("refusing to move the project root")
			}
			if from == to {
				return "", fmt.Errorf("source and destination are the same path")
			}
			info, err := os.Lstat(from)
			if err != nil {
				return "", fmt.Errorf("move %s: %w", relPath(workDir, from), err)
			}
			overwrite := boolArg(args, "overwrite")
			if dest, err := os.Lstat(to); err == nil {
				if !overwrite {
					return "", fmt.Errorf("%s already exists (set overwrite=true)", relPath(workDir, to))
				}
				if dest.IsDir() && !info.IsDir() {
					return "", fmt.Errorf("cannot replace directory %s with a file", relPath(workDir, to))
				}
			}
			if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
				return "", fmt.Errorf("create destination directory: %w", err)
			}
			if err := os.Rename(from, to); err != nil {
				// Cross-device moves and Windows' non-replacing Rename fall
				// back to an atomic copy followed by removing the source. The
				// destination was already checked above, so a fresh copy is
				// safe whether or not overwrite was requested.
				if !info.Mode().IsRegular() {
					return "", fmt.Errorf("move %s -> %s: %w", relPath(workDir, from), relPath(workDir, to), err)
				}
				if info.Size() > maxCopyBytes {
					return "", fmt.Errorf("%s is too large to move across filesystems (%s)", relPath(workDir, from), humanSize(info.Size()))
				}
				if cerr := copyFileAtomic(from, to, info.Mode().Perm(), overwrite); cerr != nil {
					return "", fmt.Errorf("move %s -> %s: %w", relPath(workDir, from), relPath(workDir, to), cerr)
				}
				_ = os.Remove(from)
			}
			return fmt.Sprintf("ok: moved %s -> %s", relPath(workDir, from), relPath(workDir, to)), nil
		},
	}
}

// NewFileCopyTool copies a file.
func NewFileCopyTool(workDir string) *Tool {
	return &Tool{
		Name:        "file_copy",
		Description: "Copy a file in the project or elsewhere. Creates the destination parent directory. Set overwrite=true to replace an existing file.",
		Parameters: objectSchema(map[string]any{
			"from":      propSchema{Type: "string", Description: "Source path relative to the project, or absolute."},
			"to":        propSchema{Type: "string", Description: "Destination path relative to the project, or absolute."},
			"overwrite": propSchema{Type: "boolean", Description: "Replace the destination if it exists."},
		}, "from", "to"),
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			from, err := resolvePath(workDir, str(args["from"]))
			if err != nil {
				return "", fmt.Errorf("source: %w", err)
			}
			to, err := resolvePath(workDir, str(args["to"]))
			if err != nil {
				return "", fmt.Errorf("destination: %w", err)
			}
			info, err := os.Stat(from)
			if err != nil {
				return "", fmt.Errorf("copy %s: %w", relPath(workDir, from), err)
			}
			if info.IsDir() {
				return "", fmt.Errorf("%s is a directory; file_copy only copies files", relPath(workDir, from))
			}
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("%s is not a regular file", relPath(workDir, from))
			}
			if info.Size() > maxCopyBytes {
				return "", fmt.Errorf("%s is too large to copy (%s)", relPath(workDir, from), humanSize(info.Size()))
			}
			overwrite := boolArg(args, "overwrite")
			if !overwrite {
				if _, err := os.Lstat(to); err == nil {
					return "", fmt.Errorf("%s already exists (set overwrite=true)", relPath(workDir, to))
				}
			}
			if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
				return "", fmt.Errorf("create destination directory: %w", err)
			}
			if err := copyFileAtomic(from, to, info.Mode().Perm(), overwrite); err != nil {
				return "", fmt.Errorf("copy %s -> %s: %w", relPath(workDir, from), relPath(workDir, to), err)
			}
			return fmt.Sprintf("ok: copied %s -> %s (%s)", relPath(workDir, from), relPath(workDir, to), humanSize(info.Size())), nil
		},
	}
}

// copyFileAtomic copies from to to without exposing a partially written
// destination. Non-overwrite copies use O_EXCL; overwrites stage into a
// temporary file in the destination directory and rename it into place.
func copyFileAtomic(from, to string, mode fs.FileMode, overwrite bool) error {
	if !overwrite {
		out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		if err := copyIntoFile(from, out); err != nil {
			out.Close()
			_ = os.Remove(to)
			return err
		}
		return nil
	}

	tmp, err := os.CreateTemp(filepath.Dir(to), ".metis-copy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := copyIntoFile(from, tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := os.Rename(tmpName, to); err != nil {
		// Windows cannot replace an existing file with Rename.
		_ = os.Remove(to)
		return os.Rename(tmpName, to)
	}
	return nil
}

func copyIntoFile(from string, out *os.File) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	if _, err := io.Copy(out, io.LimitReader(in, maxCopyBytes+1)); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	return out.Close()
}

// NewFileDeleteTool deletes a file or directory.
func NewFileDeleteTool(workDir string) *Tool {
	return &Tool{
		Name:        "file_delete",
		Description: "Delete a file in the project or elsewhere. Directories require recursive=true. The project root cannot be deleted.",
		Parameters: objectSchema(map[string]any{
			"path":      propSchema{Type: "string", Description: "Path relative to the project, or absolute."},
			"recursive": propSchema{Type: "boolean", Description: "Delete a directory and everything inside it."},
		}, "path"),
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			p, err := resolvePath(workDir, str(args["path"]))
			if err != nil {
				return "", err
			}
			if isSandboxRoot(workDir, p) {
				return "", fmt.Errorf("refusing to delete the project root")
			}
			info, err := os.Lstat(p)
			if err != nil {
				return "", fmt.Errorf("delete %s: %w", relPath(workDir, p), err)
			}
			recursive := boolArg(args, "recursive")
			if info.IsDir() {
				if !recursive {
					return "", fmt.Errorf("%s is a directory (set recursive=true)", relPath(workDir, p))
				}
				if err := os.RemoveAll(p); err != nil {
					return "", fmt.Errorf("delete %s: %w", relPath(workDir, p), err)
				}
				return fmt.Sprintf("ok: deleted directory %s", relPath(workDir, p)), nil
			}
			if err := os.Remove(p); err != nil {
				return "", fmt.Errorf("delete %s: %w", relPath(workDir, p), err)
			}
			return fmt.Sprintf("ok: deleted %s", relPath(workDir, p)), nil
		},
	}
}

// NewMultiEditTool applies several replacements atomically.
func NewMultiEditTool(workDir string) *Tool {
	editItem := objectSchema(map[string]any{
		"old_text":    map[string]any{"type": "string", "description": "Exact text to replace."},
		"new_text":    map[string]any{"type": "string", "description": "Replacement text (may be empty)."},
		"replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence."},
	}, "old_text")
	return &Tool{
		Name: "multi_edit",
		Description: "Apply several exact replacements to one file atomically. Every edit must match before anything is written. " +
			"Use this for related changes that belong together.",
		Parameters: objectSchema(map[string]any{
			"path": propSchema{Type: "string", Description: "File path relative to the project, or absolute."},
			"edits": map[string]any{
				"type":        "array",
				"description": "List of replacements, applied in order.",
				"minItems":    1,
				"items":       editItem,
			},
		}, "path", "edits"),
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			p, err := resolvePath(workDir, str(args["path"]))
			if err != nil {
				return "", err
			}
			edits, err := parseEditSpecs(args["edits"])
			if err != nil {
				return "", err
			}
			info, err := os.Stat(p)
			if err != nil {
				return "", fmt.Errorf("edit %s: %w", p, err)
			}
			if info.IsDir() {
				return "", fmt.Errorf("%s is a directory", relPath(workDir, p))
			}
			if info.Size() > maxEditBytes {
				return "", fmt.Errorf("%s is too large to edit (%s)", relPath(workDir, p), humanSize(info.Size()))
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return "", fmt.Errorf("edit %s: %w", relPath(workDir, p), err)
			}
			if bytes.IndexByte(b, 0) >= 0 {
				return "", fmt.Errorf("%s looks like a binary file", relPath(workDir, p))
			}
			original := string(b)
			content := original
			replacements := 0
			for i, e := range edits {
				count := strings.Count(content, e.oldText)
				switch {
				case count == 0:
					return "", fmt.Errorf("edit %d: old_text was not found in %s", i+1, relPath(workDir, p))
				case count > 1 && !e.replaceAll:
					return "", fmt.Errorf("edit %d: old_text appears %d times; set replace_all=true", i+1, count)
				}
				if e.replaceAll {
					content = strings.ReplaceAll(content, e.oldText, e.newText)
					replacements += count
				} else {
					content = strings.Replace(content, e.oldText, e.newText, 1)
					replacements++
				}
			}
			if err := os.WriteFile(p, []byte(content), info.Mode().Perm()); err != nil {
				return "", fmt.Errorf("write %s: %w", relPath(workDir, p), err)
			}
			msg := fmt.Sprintf("ok: applied %d edit(s) to %s (%d replacement(s))",
				len(edits), relPath(workDir, p), replacements)
			if d := unifiedDiff(original, content, relPath(workDir, p)); d != "" {
				msg += "\n" + d
			}
			return msg, nil
		},
	}
}

type editSpec struct {
	oldText    string
	newText    string
	replaceAll bool
}

func parseEditSpecs(v any) ([]editSpec, error) {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil, fmt.Errorf("edits must be a non-empty array")
	}
	out := make([]editSpec, 0, len(arr))
	for i, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("edit %d must be an object", i+1)
		}
		oldText := str(m["old_text"])
		if oldText == "" {
			return nil, fmt.Errorf("edit %d: old_text is required", i+1)
		}
		out = append(out, editSpec{
			oldText:    oldText,
			newText:    str(m["new_text"]),
			replaceAll: boolArg(m, "replace_all"),
		})
	}
	return out, nil
}

func isSandboxRoot(workDir, p string) bool {
	root, err := filepath.Abs(workDir)
	if err != nil {
		return false
	}
	root = resolveSymlinks(root)
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	return resolveSymlinks(abs) == root
}

// NewFileReadManyTool reads several files in one call.
func NewFileReadManyTool(workDir string) *Tool {
	return &Tool{
		Name: "file_read_many",
		Description: "Read several text files in one call. Each file is prefixed with its path. " +
			"Use this to inspect related files together instead of calling file_read repeatedly.",
		Parameters: objectSchema(map[string]any{
			"paths": map[string]any{
				"type":        "array",
				"description": "File paths relative to the project, or absolute.",
				"items":       map[string]any{"type": "string"},
				"minItems":    1,
				"maxItems":    50,
			},
			"start_line":         propSchema{Type: "integer", Description: "Optional first line for every file (1-based)."},
			"end_line":           propSchema{Type: "integer", Description: "Optional last line for every file (inclusive)."},
			"max_bytes_per_file": propSchema{Type: "integer", Description: "Per-file cap in bytes (default 65536, max 524288)."},
		}, "paths"),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			paths := stringList(args["paths"])
			if len(paths) == 0 {
				return "", fmt.Errorf("paths must be a non-empty array")
			}
			if len(paths) > 50 {
				return "", fmt.Errorf("too many paths (%d); request at most 50 files", len(paths))
			}
			start := intArg(args, "start_line", 0)
			end := intArg(args, "end_line", 0)
			perFile := clamp(intArg(args, "max_bytes_per_file", 64*1024), 1024, maxReadBytes)
			const totalCap = 128 * 1024

			var b strings.Builder
			for _, rel := range paths {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				fmt.Fprintf(&b, "=== %s ===\n", rel)
				p, err := resolvePath(workDir, rel)
				if err != nil {
					fmt.Fprintf(&b, "error: %v\n\n", err)
					continue
				}
				info, err := os.Stat(p)
				if err != nil {
					fmt.Fprintf(&b, "error: %v\n\n", err)
					continue
				}
				if info.IsDir() {
					fmt.Fprintf(&b, "error: %s is a directory\n\n", relPath(workDir, p))
					continue
				}
				if isBinaryFile(p) {
					fmt.Fprintf(&b, "error: binary file\n\n")
					continue
				}
				var content string
				if start > 0 || end > 0 {
					content, err = readLineRange(p, start, end)
				} else {
					var data []byte
					data, err = os.ReadFile(p)
					content = string(data)
				}
				if err != nil {
					fmt.Fprintf(&b, "error: %v\n\n", err)
					continue
				}
				if len(content) > perFile {
					content = truncateText(content, perFile) +
						fmt.Sprintf("\n(file is %s; use start_line/end_line to read a range)", humanSize(info.Size()))
				}
				b.WriteString(content)
				if !strings.HasSuffix(content, "\n") {
					b.WriteByte('\n')
				}
				b.WriteByte('\n')
				if b.Len() >= totalCap {
					b.WriteString("... (batch output truncated; request fewer files or use line ranges)\n")
					break
				}
			}
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}

// NewDiffFilesTool compares two text files.
func NewDiffFilesTool(workDir string) *Tool {
	return &Tool{
		Name:        "diff_files",
		Description: "Show a unified diff between two text files in the project or elsewhere.",
		Parameters: objectSchema(map[string]any{
			"a":       propSchema{Type: "string", Description: "First file path relative to the project, or absolute."},
			"b":       propSchema{Type: "string", Description: "Second file path relative to the project, or absolute."},
			"context": propSchema{Type: "integer", Description: "Context lines (default 3, max 20)."},
		}, "a", "b"),
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			a, err := resolvePath(workDir, str(args["a"]))
			if err != nil {
				return "", fmt.Errorf("a: %w", err)
			}
			b, err := resolvePath(workDir, str(args["b"]))
			if err != nil {
				return "", fmt.Errorf("b: %w", err)
			}
			before, err := readComparableFile(a)
			if err != nil {
				return "", fmt.Errorf("a: %w", err)
			}
			after, err := readComparableFile(b)
			if err != nil {
				return "", fmt.Errorf("b: %w", err)
			}
			if before == after {
				return fmt.Sprintf("files are identical (%s)", humanSize(int64(len(before)))), nil
			}
			d, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
				A:        difflib.SplitLines(before),
				B:        difflib.SplitLines(after),
				FromFile: "a/" + relPath(workDir, a),
				ToFile:   "b/" + relPath(workDir, b),
				Context:  clamp(intArg(args, "context", 3), 1, 20),
			})
			if err != nil {
				return "", fmt.Errorf("diff: %w", err)
			}
			if strings.TrimSpace(d) == "" {
				return "files differ only in trailing newlines or line endings", nil
			}
			return capDiff(d), nil
		},
	}
}

func readComparableFile(p string) (string, error) {
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", p)
	}
	if info.Size() > maxEditBytes {
		return "", fmt.Errorf("%s is too large to compare (%s)", p, humanSize(info.Size()))
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return "", fmt.Errorf("%s looks like a binary file", p)
	}
	return string(b), nil
}

// NewReplaceInFilesTool applies a regex or literal replacement across a tree.
func NewReplaceInFilesTool(workDir string) *Tool {
	return &Tool{
		Name: "replace_in_files",
		Description: "Apply a regular expression (or literal text) replacement across many files in one pass. " +
			"Use dry_run=true to preview the affected files first. Skips binary files and noisy directories.",
		Parameters: objectSchema(map[string]any{
			"pattern":     propSchema{Type: "string", Description: "Regular expression (or literal text when literal=true)."},
			"replacement": propSchema{Type: "string", Description: "Replacement text. Regex groups use $1, ${name}."},
			"path":        propSchema{Type: "string", Description: "File or directory relative to the project, or absolute (default .)."},
			"include":     propSchema{Type: "string", Description: "Optional glob filter, e.g. *.go."},
			"literal":     propSchema{Type: "boolean", Description: "Treat pattern as literal text, not a regex."},
			"dry_run":     propSchema{Type: "boolean", Description: "Preview changes without writing."},
			"max_files":   propSchema{Type: "integer", Description: "Maximum files to change (default 200, max 1000)."},
		}, "pattern", "replacement"),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			patternArg := str(args["pattern"])
			if patternArg == "" {
				return "", fmt.Errorf("pattern is required")
			}
			if !hasArg(args, "replacement") {
				return "", fmt.Errorf("replacement is required (use an empty string to delete matches)")
			}
			repl := str(args["replacement"])
			literal := boolArg(args, "literal")
			if literal {
				patternArg = regexp.QuoteMeta(patternArg)
			}
			re, err := regexp.Compile(patternArg)
			if err != nil {
				return "", fmt.Errorf("invalid pattern: %w", err)
			}
			root, err := resolvePath(workDir, defaultString(str(args["path"]), "."))
			if err != nil {
				return "", err
			}
			info, err := os.Stat(root)
			if err != nil {
				return "", fmt.Errorf("replace %s: %w", root, err)
			}
			include := str(args["include"])
			dryRun := boolArg(args, "dry_run")
			maxFiles := clamp(intArg(args, "max_files", 200), 1, 1000)

			var files []string
			if info.IsDir() {
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
					if include != "" && !matchInclude(include, filepath.ToSlash(rel)) {
						return nil
					}
					fi, err := d.Info()
					if err != nil || fi.Size() > maxEditBytes {
						return nil
					}
					files = append(files, p)
					if len(files) >= maxFiles {
						return fs.SkipAll
					}
					return nil
				})
				if walkErr != nil && walkErr != fs.SkipAll {
					return "", fmt.Errorf("replace %s: %w", root, walkErr)
				}
			} else {
				files = append(files, root)
			}

			var changed []string
			total := 0
			var preview strings.Builder
			for _, p := range files {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				b, err := os.ReadFile(p)
				if err != nil || bytes.IndexByte(b, 0) >= 0 {
					continue
				}
				original := string(b)
				updated := re.ReplaceAllString(original, repl)
				if literal {
					updated = re.ReplaceAllLiteralString(original, repl)
				}
				if updated == original {
					continue
				}
				n := len(re.FindAllStringIndex(original, -1))
				rel := relPath(workDir, p)
				changed = append(changed, fmt.Sprintf("%s: %d replacement(s)", rel, n))
				total += n
				if len(changed) <= 3 {
					preview.WriteString(unifiedDiff(original, updated, rel))
					preview.WriteByte('\n')
				}
				if !dryRun {
					fi, err := os.Stat(p)
					if err != nil {
						return "", fmt.Errorf("stat %s: %w", rel, err)
					}
					if err := os.WriteFile(p, []byte(updated), fi.Mode().Perm()); err != nil {
						return "", fmt.Errorf("write %s: %w", rel, err)
					}
				}
			}
			if len(changed) == 0 {
				return fmt.Sprintf("no matches for %q", patternArg), nil
			}
			verb := "replaced"
			if dryRun {
				verb = "would replace"
			}
			var b strings.Builder
			fmt.Fprintf(&b, "ok: %s %d occurrence(s) in %d file(s)", verb, total, len(changed))
			if len(files) >= maxFiles {
				fmt.Fprintf(&b, " (stopped after %d files)", maxFiles)
			}
			b.WriteByte('\n')
			b.WriteString(strings.Join(changed, "\n"))
			if preview.Len() > 0 {
				b.WriteString("\n\n")
				b.WriteString(strings.TrimRight(preview.String(), "\n"))
			}
			return b.String(), nil
		},
	}
}

// NewHashFileTool hashes a file, optionally checking an expected digest.
func NewHashFileTool(workDir string) *Tool {
	return &Tool{
		Name:        "hash_file",
		Description: "Compute a file's MD5, SHA-1, SHA-256, or SHA-512 digest. Optionally compare it with an expected value.",
		Parameters: objectSchema(map[string]any{
			"path":      propSchema{Type: "string", Description: "File path relative to the project, or absolute."},
			"algorithm": propSchema{Type: "string", Description: "sha256 (default), sha1, sha512, or md5."},
			"expected":  propSchema{Type: "string", Description: "Optional expected hex digest to compare against."},
		}, "path"),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			p, err := resolvePath(workDir, str(args["path"]))
			if err != nil {
				return "", err
			}
			info, err := os.Stat(p)
			if err != nil {
				return "", fmt.Errorf("hash %s: %w", relPath(workDir, p), err)
			}
			if info.IsDir() {
				return "", fmt.Errorf("%s is a directory", relPath(workDir, p))
			}
			algo := strings.ToLower(defaultString(str(args["algorithm"]), "sha256"))
			var h hash.Hash
			switch algo {
			case "sha256", "sha-256":
				h, algo = sha256.New(), "sha256"
			case "sha1", "sha-1":
				h, algo = sha1.New(), "sha1"
			case "sha512", "sha-512":
				h, algo = sha512.New(), "sha512"
			case "md5":
				h = md5.New()
			default:
				return "", fmt.Errorf("unsupported algorithm %q (use sha256, sha1, sha512, or md5)", algo)
			}
			f, err := os.Open(p)
			if err != nil {
				return "", fmt.Errorf("hash %s: %w", relPath(workDir, p), err)
			}
			defer f.Close()
			buf := make([]byte, 64*1024)
			for {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				n, err := f.Read(buf)
				if n > 0 {
					_, _ = h.Write(buf[:n])
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					return "", fmt.Errorf("read %s: %w", relPath(workDir, p), err)
				}
			}
			digest := hex.EncodeToString(h.Sum(nil))
			var b strings.Builder
			fmt.Fprintf(&b, "path: %s\n", relPath(workDir, p))
			fmt.Fprintf(&b, "algorithm: %s\n", algo)
			fmt.Fprintf(&b, "size: %s (%d bytes)\n", humanSize(info.Size()), info.Size())
			fmt.Fprintf(&b, "digest: %s\n", digest)
			if expected := strings.TrimSpace(str(args["expected"])); expected != "" {
				if strings.EqualFold(expected, digest) {
					b.WriteString("match: yes\n")
				} else {
					fmt.Fprintf(&b, "match: no (expected %s)\n", expected)
				}
			}
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}
