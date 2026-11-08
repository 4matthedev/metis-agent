package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// diagnosticCommand is one language-appropriate check.
type diagnosticCommand struct {
	Name string
	Args []string
}

// NewDiagnosticsTool runs the build, type-checker, or linter that matches the
// project. It executes programs, so it is approval-gated like shell.
func NewDiagnosticsTool(workDir string) *Tool {
	return &Tool{
		Name: "diagnostics",
		Description: "Run the project's native build, type-check, and lint checks (Go build/vet, tsc, ruff/compileall, " +
			"cargo check, and similar). Use it to verify changes without guessing the right command. " +
			"The check list is detected from manifests; pass commands to override it.",
		Parameters: objectSchema(map[string]any{
			"path": propSchema{Type: "string", Description: "Project directory relative to the project, or absolute (default .)."},
			"commands": map[string]any{
				"type":        "array",
				"description": "Optional explicit commands to run instead of detection.",
				"items":       map[string]any{"type": "string"},
				"maxItems":    10,
			},
			"timeout_sec": propSchema{Type: "integer", Description: "Per-command timeout in seconds (default 180, max 900)."},
			"stop_on_failure": propSchema{Type: "boolean",
				Description: "Stop after the first failing command (default true)."},
		}),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			root, err := resolvePath(workDir, defaultString(str(args["path"]), "."))
			if err != nil {
				return "", err
			}
			info, err := os.Stat(root)
			if err != nil {
				return "", fmt.Errorf("diagnostics %s: %w", root, err)
			}
			if !info.IsDir() {
				return "", fmt.Errorf("%s is not a directory", relPath(workDir, root))
			}
			timeoutSec := clamp(intArg(args, "timeout_sec", 180), 1, 900)
			stopOnFailure := true
			if hasArg(args, "stop_on_failure") {
				stopOnFailure = boolArg(args, "stop_on_failure")
			}

			var cmds []diagnosticCommand
			if explicit := stringList(args["commands"]); len(explicit) > 0 {
				for _, line := range explicit {
					fields := strings.Fields(line)
					if len(fields) == 0 {
						continue
					}
					cmds = append(cmds, diagnosticCommand{Name: fields[0], Args: fields[1:]})
				}
			} else {
				cmds = detectDiagnostics(root)
			}
			if len(cmds) == 0 {
				return "no diagnostics were detected for this project; pass commands explicitly", nil
			}

			var b strings.Builder
			failures := 0
			for _, c := range cmds {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				label := strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
				runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
				cmd := exec.CommandContext(runCtx, c.Name, c.Args...)
				cmd.Dir = root
				var out bytes.Buffer
				cmd.Stdout = &out
				cmd.Stderr = &out
				err := cmd.Run()
				cancel()

				status := "ok"
				if err != nil {
					status = "FAILED"
					failures++
					if runCtx.Err() == context.DeadlineExceeded {
						status = fmt.Sprintf("FAILED (timed out after %ds)", timeoutSec)
					}
				}
				fmt.Fprintf(&b, "$ %s\n[%s]\n", label, status)
				text := strings.TrimRight(strings.ToValidUTF8(out.String(), ""), "\n")
				if text != "" {
					b.WriteString(truncateText(text, 32*1024))
					b.WriteByte('\n')
				}
				b.WriteByte('\n')
				if err != nil && stopOnFailure {
					break
				}
			}
			if failures == 0 {
				b.WriteString("all checks passed")
			} else {
				fmt.Fprintf(&b, "%d check(s) failed", failures)
			}
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}

// detectDiagnostics picks checks that are available and appropriate.
func detectDiagnostics(root string) []diagnosticCommand {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(root, name))
		return err == nil
	}
	have := func(name string) bool {
		_, err := exec.LookPath(name)
		return err == nil
	}
	var out []diagnosticCommand

	switch {
	case exists("go.mod") && have("go"):
		out = append(out,
			diagnosticCommand{Name: "go", Args: []string{"build", "./..."}},
			diagnosticCommand{Name: "go", Args: []string{"vet", "./..."}},
		)
		if have("gofmt") {
			out = append(out, diagnosticCommand{Name: "gofmt", Args: []string{"-l", "."}})
		}
	case exists("Cargo.toml") && have("cargo"):
		out = append(out, diagnosticCommand{Name: "cargo", Args: []string{"check", "--quiet"}})
	case exists("tsconfig.json") && have("npx"):
		out = append(out, diagnosticCommand{Name: "npx", Args: []string{"--no-install", "tsc", "--noEmit"}})
	case (exists("pyproject.toml") || exists("requirements.txt") || exists("setup.py")):
		if have("ruff") {
			out = append(out, diagnosticCommand{Name: "ruff", Args: []string{"check", "."}})
		}
		py := "python3"
		if !have(py) {
			py = "python"
		}
		if have(py) {
			out = append(out, diagnosticCommand{Name: py, Args: []string{"-m", "compileall", "-q", "."}})
		}
	case exists("package.json") && have("npm"):
		out = append(out, diagnosticCommand{Name: "npm", Args: []string{"run", "--silent", "lint", "--if-present"}})
	}
	return out
}
