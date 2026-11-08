package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// runGit runs git with structured arguments (never through a shell) inside dir.
func runGit(ctx context.Context, dir string, timeoutSec int, args ...string) (string, int, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return "", 0, fmt.Errorf("git is not installed or not on PATH")
	}
	timeoutSec = clamp(timeoutSec, 1, 300)
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	out, err := os.CreateTemp("", "metis-git-*")
	if err != nil {
		return "", 0, fmt.Errorf("create output file: %w", err)
	}
	outPath := out.Name()
	defer os.Remove(outPath)
	defer out.Close()

	fullArgs := append([]string{"-C", dir, "--no-pager"}, args...)
	cmd := exec.CommandContext(runCtx, git, fullArgs...)
	configureProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Env = append(os.Environ(),
		"GIT_PAGER=cat",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_EDITOR=true",
		"NO_COLOR=1",
		"TERM=dumb",
		"LC_ALL=C",
	)

	runErr := cmd.Run()
	_ = out.Sync()
	output, readErr := readCapturedOutput(outPath, 64*1024)
	if readErr != nil {
		return "", 0, readErr
	}
	output = cleanCommandOutput(output)

	if ctx.Err() != nil {
		return "", 0, ctx.Err()
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return "", 0, fmt.Errorf("git timed out after %ds\n%s", timeoutSec, output)
	}

	code := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			return "", 0, fmt.Errorf("run git: %w", runErr)
		}
	}
	return output, code, nil
}

func gitResult(output string, code int) (string, error) {
	if code != 0 {
		if strings.TrimSpace(output) == "" {
			return fmt.Sprintf("exit code: %d (git failed with no output)", code), nil
		}
		return fmt.Sprintf("exit code: %d\n%s", code, strings.TrimRight(output, "\n")), nil
	}
	if strings.TrimSpace(output) == "" {
		return "(no output)", nil
	}
	return strings.TrimRight(output, "\n"), nil
}

func gitDir(workDir, pathArg string) (string, error) {
	dir, err := resolvePath(workDir, defaultString(strings.TrimSpace(pathArg), "."))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("git path %s: %w", relPath(workDir, dir), err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", relPath(workDir, dir))
	}
	return dir, nil
}

func gitFileSpec(workDir, baseDir, file string) (string, error) {
	if strings.TrimSpace(file) == "" {
		return "", nil
	}
	p, err := resolvePath(workDir, file)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(baseDir, p)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", file, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the git path", file)
	}
	return filepath.ToSlash(rel), nil
}

// NewGitStatusTool reports the working tree status.
func NewGitStatusTool(workDir string) *Tool {
	return &Tool{
		Name:        "git_status",
		Description: "Show git working tree status with branch information (git status --short --branch).",
		Parameters: objectSchema(map[string]any{
			"path":        propSchema{Type: "string", Description: "Repository subdirectory relative to the project, or absolute (default .)."},
			"timeout_sec": propSchema{Type: "integer", Description: "Timeout in seconds (default 30)."},
		}),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			dir, err := gitDir(workDir, str(args["path"]))
			if err != nil {
				return "", err
			}
			out, code, err := runGit(ctx, dir, intArg(args, "timeout_sec", 30), "status", "--short", "--branch")
			if err != nil {
				return "", err
			}
			if code == 0 && strings.TrimSpace(out) == "" {
				return "clean working tree", nil
			}
			return gitResult(out, code)
		},
	}
}

// NewGitDiffTool shows unstaged or staged changes.
func NewGitDiffTool(workDir string) *Tool {
	return &Tool{
		Name:        "git_diff",
		Description: "Show a git diff. Set staged=true for the index, stat=true for a summary, and file to limit the diff.",
		Parameters: objectSchema(map[string]any{
			"path":        propSchema{Type: "string", Description: "Repository subdirectory relative to the project, or absolute (default .)."},
			"file":        propSchema{Type: "string", Description: "Optional file path relative to the project, or absolute."},
			"staged":      propSchema{Type: "boolean", Description: "Show staged (cached) changes."},
			"stat":        propSchema{Type: "boolean", Description: "Show a diffstat instead of the full diff."},
			"timeout_sec": propSchema{Type: "integer", Description: "Timeout in seconds (default 30)."},
		}),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			dir, err := gitDir(workDir, str(args["path"]))
			if err != nil {
				return "", err
			}
			file, err := gitFileSpec(workDir, dir, str(args["file"]))
			if err != nil {
				return "", err
			}
			gitArgs := []string{"diff", "--no-color", "--no-ext-diff"}
			if boolArg(args, "staged") {
				gitArgs = append(gitArgs, "--cached")
			}
			if boolArg(args, "stat") {
				gitArgs = append(gitArgs, "--stat")
			}
			if file != "" {
				gitArgs = append(gitArgs, "--", file)
			}
			out, code, err := runGit(ctx, dir, intArg(args, "timeout_sec", 30), gitArgs...)
			if err != nil {
				return "", err
			}
			return gitResult(out, code)
		},
	}
}

// NewGitLogTool shows recent commits.
func NewGitLogTool(workDir string) *Tool {
	return &Tool{
		Name:        "git_log",
		Description: "Show recent commits with hash, date, author, and subject. Optionally limit to a file or all branches.",
		Parameters: objectSchema(map[string]any{
			"path":        propSchema{Type: "string", Description: "Repository subdirectory relative to the project, or absolute (default .)."},
			"file":        propSchema{Type: "string", Description: "Optional file path relative to the project, or absolute."},
			"limit":       propSchema{Type: "integer", Description: "Number of commits (default 20, max 200)."},
			"all":         propSchema{Type: "boolean", Description: "Include all branches."},
			"author":      propSchema{Type: "string", Description: "Optional author filter."},
			"full":        propSchema{Type: "boolean", Description: "Show fuller commit messages instead of one line each."},
			"timeout_sec": propSchema{Type: "integer", Description: "Timeout in seconds (default 30)."},
		}),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			dir, err := gitDir(workDir, str(args["path"]))
			if err != nil {
				return "", err
			}
			file, err := gitFileSpec(workDir, dir, str(args["file"]))
			if err != nil {
				return "", err
			}
			limit := clamp(intArg(args, "limit", 20), 1, 200)
			gitArgs := []string{"log", "--no-color", fmt.Sprintf("-n%d", limit)}
			if boolArg(args, "full") {
				gitArgs = append(gitArgs, "--format=fuller")
			} else {
				gitArgs = append(gitArgs, "--date=short", "--pretty=format:%h %ad %an%d: %s")
			}
			if boolArg(args, "all") {
				gitArgs = append(gitArgs, "--all")
			}
			if author := strings.TrimSpace(str(args["author"])); author != "" {
				gitArgs = append(gitArgs, "--author="+author)
			}
			if file != "" {
				gitArgs = append(gitArgs, "--", file)
			}
			out, code, err := runGit(ctx, dir, intArg(args, "timeout_sec", 30), gitArgs...)
			if err != nil {
				return "", err
			}
			return gitResult(out, code)
		},
	}
}

// NewGitShowTool shows a commit or object.
func NewGitShowTool(workDir string) *Tool {
	return &Tool{
		Name:        "git_show",
		Description: "Show a commit, tag, or other git object. Set stat=true for a summary, or file to show one path at that revision.",
		Parameters: objectSchema(map[string]any{
			"rev":         propSchema{Type: "string", Description: "Revision (default HEAD)."},
			"path":        propSchema{Type: "string", Description: "Repository subdirectory relative to the project, or absolute (default .)."},
			"file":        propSchema{Type: "string", Description: "Optional file path relative to the project, or absolute."},
			"stat":        propSchema{Type: "boolean", Description: "Show a diffstat instead of the full patch."},
			"timeout_sec": propSchema{Type: "integer", Description: "Timeout in seconds (default 30)."},
		}),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			dir, err := gitDir(workDir, str(args["path"]))
			if err != nil {
				return "", err
			}
			rev := defaultString(strings.TrimSpace(str(args["rev"])), "HEAD")
			if strings.HasPrefix(rev, "-") || strings.ContainsAny(rev, " \t\n") {
				return "", fmt.Errorf("invalid revision %q", rev)
			}
			file, err := gitFileSpec(workDir, dir, str(args["file"]))
			if err != nil {
				return "", err
			}
			gitArgs := []string{"show", "--no-color", "--no-ext-diff", "--format=fuller"}
			if boolArg(args, "stat") {
				gitArgs = append(gitArgs, "--stat")
			}
			gitArgs = append(gitArgs, rev)
			if file != "" {
				gitArgs = append(gitArgs, "--", file)
			}
			out, code, err := runGit(ctx, dir, intArg(args, "timeout_sec", 30), gitArgs...)
			if err != nil {
				return "", err
			}
			return gitResult(out, code)
		},
	}
}

// NewGitBlameTool shows line-by-line authorship.
func NewGitBlameTool(workDir string) *Tool {
	return &Tool{
		Name:        "git_blame",
		Description: "Show line-by-line commit and author information for a file (git blame), optionally for a line range.",
		Parameters: objectSchema(map[string]any{
			"file":        propSchema{Type: "string", Description: "File path relative to the project, or absolute."},
			"path":        propSchema{Type: "string", Description: "Repository subdirectory relative to the project, or absolute (default .)."},
			"start_line":  propSchema{Type: "integer", Description: "Optional first line (1-based)."},
			"end_line":    propSchema{Type: "integer", Description: "Optional last line (inclusive)."},
			"timeout_sec": propSchema{Type: "integer", Description: "Timeout in seconds (default 30)."},
		}, "file"),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			dir, err := gitDir(workDir, str(args["path"]))
			if err != nil {
				return "", err
			}
			file, err := gitFileSpec(workDir, dir, str(args["file"]))
			if err != nil {
				return "", err
			}
			if file == "" {
				return "", fmt.Errorf("file is required")
			}
			abs := filepath.Join(dir, filepath.FromSlash(file))
			if _, err := os.Stat(abs); err != nil {
				return "", fmt.Errorf("blame %s: %w", file, err)
			}
			gitArgs := []string{"blame", "--date=short"}
			start := intArg(args, "start_line", 0)
			end := intArg(args, "end_line", 0)
			if start > 0 || end > 0 {
				if start < 1 {
					start = 1
				}
				if end < start {
					end = start
				}
				gitArgs = append(gitArgs, fmt.Sprintf("-L%d,%d", start, end))
			}
			gitArgs = append(gitArgs, "--", file)
			out, code, err := runGit(ctx, dir, intArg(args, "timeout_sec", 30), gitArgs...)
			if err != nil {
				return "", err
			}
			return gitResult(out, code)
		},
	}
}
