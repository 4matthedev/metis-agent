package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const (
	defaultShellTimeout = 120 * time.Second
	defaultMaxOutput    = 24 * 1024
)

// ansiRE matches common ANSI CSI/OSC sequences so command output does not
// pollute the model context or the transcript.
var ansiRE = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)

func NewShellTool(workDir string, defaultTimeout time.Duration, maxOutput int) *Tool {
	if defaultTimeout <= 0 {
		defaultTimeout = defaultShellTimeout
	}
	if maxOutput <= 0 {
		maxOutput = defaultMaxOutput
	}
	defaultSec := int(defaultTimeout.Seconds())
	return &Tool{
		Name: "shell",
		Description: "Run a non-interactive shell command in the project working directory. " +
			"Use it to build, test, run programs, inspect git, install dependencies, and so on. " +
			"Returns the exit code and combined stdout/stderr. Output is capped; " +
			"use flags like --quiet or tail when it is very large.",
		Parameters: objectSchema(map[string]any{
			"command":     propSchema{Type: "string", Description: "Shell command to run (bash -c)."},
			"cwd":         propSchema{Type: "string", Description: "Directory relative to the project, or absolute (default .)."},
			"timeout_sec": propSchema{Type: "integer", Description: fmt.Sprintf("Timeout in seconds (default %d, max 900).", defaultSec)},
		}, "command"),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			command := strings.TrimSpace(str(args["command"]))
			if command == "" {
				return "", fmt.Errorf("command is required")
			}
			dir, err := resolvePath(workDir, defaultString(str(args["cwd"]), "."))
			if err != nil {
				return "", err
			}
			info, err := os.Stat(dir)
			if err != nil {
				return "", fmt.Errorf("cwd %s: %w", dir, err)
			}
			if !info.IsDir() {
				return "", fmt.Errorf("cwd %s is not a directory", dir)
			}
			timeoutSec := clamp(intArg(args, "timeout_sec", defaultSec), 1, 900)
			runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
			defer cancel()

			out, err := os.CreateTemp("", "metis-shell-*")
			if err != nil {
				return "", fmt.Errorf("create output file: %w", err)
			}
			outPath := out.Name()
			defer os.Remove(outPath)
			defer out.Close()

			shell := "bash"
			if _, err := exec.LookPath(shell); err != nil {
				shell = "sh"
				if _, err := exec.LookPath(shell); err != nil {
					return "", fmt.Errorf("no shell found (tried bash and sh)")
				}
			}
			cmd := exec.CommandContext(runCtx, shell, "-c", command)
			configureProcessGroup(cmd)
			cmd.Cancel = func() error { return killProcessGroup(cmd) }
			cmd.WaitDelay = 5 * time.Second
			cmd.Dir = dir
			cmd.Stdout = out
			cmd.Stderr = out
			cmd.Env = append(os.Environ(),
				"PAGER=cat",
				"GIT_PAGER=cat",
				"GIT_EDITOR=true",
				"TERM=dumb",
				"NO_COLOR=1",
			)

			runErr := cmd.Run()
			_ = out.Sync()
			output, readErr := readCapturedOutput(outPath, maxOutput)
			if readErr != nil {
				return "", readErr
			}
			output = cleanCommandOutput(output)

			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				return "", fmt.Errorf("command timed out after %ds\n%s", timeoutSec, output)
			}

			code := 0
			if runErr != nil {
				var exitErr *exec.ExitError
				if errors.As(runErr, &exitErr) {
					code = exitErr.ExitCode()
				} else {
					return "", fmt.Errorf("run command: %w", runErr)
				}
			}

			var b strings.Builder
			fmt.Fprintf(&b, "exit code: %d\n", code)
			if strings.TrimSpace(output) == "" {
				b.WriteString("(no output)")
			} else {
				b.WriteString(output)
			}
			return b.String(), nil
		},
	}
}

// readCapturedOutput reads at most max bytes, keeping the head and the tail
// when the command produced more output than that.
func readCapturedOutput(path string, max int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read command output: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat command output: %w", err)
	}
	size := info.Size()
	if size <= int64(max) {
		b, err := io.ReadAll(f)
		if err != nil {
			return "", fmt.Errorf("read command output: %w", err)
		}
		return strings.ToValidUTF8(string(b), ""), nil
	}

	headLen := max * 2 / 3
	tailLen := max - headLen
	head := make([]byte, headLen)
	if _, err := f.ReadAt(head, 0); err != nil && err != io.EOF {
		return "", fmt.Errorf("read command output: %w", err)
	}
	tail := make([]byte, tailLen)
	if _, err := f.ReadAt(tail, size-int64(tailLen)); err != nil && err != io.EOF {
		return "", fmt.Errorf("read command output: %w", err)
	}
	return fmt.Sprintf("%s\n... [%d bytes truncated] ...\n%s",
		strings.ToValidUTF8(string(head), ""),
		size-int64(max),
		strings.ToValidUTF8(string(tail), ""),
	), nil
}

// cleanCommandOutput strips ANSI escapes and control characters while keeping
// newlines and tabs.
func cleanCommandOutput(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			// drop
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
