package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"metis-agent/internal/agent"
	"metis-agent/internal/checkpoint"
	"metis-agent/internal/llm"
	"metis-agent/internal/tools"
)

// splitCSV splits a comma-separated flag value.
func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// jsonSink writes one JSON object per line for editor and tool integration.
func jsonSink() func(agent.Event) {
	enc := json.NewEncoder(os.Stdout)
	var mu sync.Mutex
	return func(ev agent.Event) {
		event := map[string]any{"step": ev.Step}
		switch ev.Kind {
		case agent.EventStep:
			event["type"] = "step"
		case agent.EventText:
			event["type"] = "text"
			event["text"] = ev.Text
		case agent.EventToolCall:
			event["type"] = "tool_call"
			event["tool"] = ev.Tool
			event["args"] = rawJSON(ev.Args)
		case agent.EventToolResult:
			event["type"] = "tool_result"
			event["tool"] = ev.Tool
			event["result"] = ev.Result
			event["duration_ms"] = ev.Duration.Milliseconds()
		case agent.EventDone:
			event["type"] = "done"
			event["text"] = ev.Text
		case agent.EventError:
			event["type"] = "error"
			if ev.Err != nil {
				event["error"] = ev.Err.Error()
			}
		default:
			return
		}
		mu.Lock()
		_ = enc.Encode(event)
		mu.Unlock()
	}
}

func rawJSON(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	return v
}

// undoFunc restores the last checkpoint.
func undoFunc(cp *checkpoint.Manager) func() (string, error) {
	if cp == nil {
		return nil
	}
	return func() (string, error) {
		res, err := cp.Undo()
		if err != nil {
			return "", err
		}
		msg := fmt.Sprintf("restored %d path(s) from %s", res.Restored, filepath.Base(res.Directory))
		if len(res.Skipped) > 0 {
			msg += "\nnot restored:\n  " + strings.Join(res.Skipped, "\n  ")
		}
		return msg, nil
	}
}

func listModelNames(r *llm.Router) func(context.Context) ([]string, error) {
	if r == nil {
		return nil
	}
	return func(ctx context.Context) ([]string, error) {
		models, err := r.ListModels(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(models))
		for _, m := range models {
			out = append(out, m.ID)
		}
		return out, nil
	}
}

// runVerifyCommand executes the configured auto-verify command.
func runVerifyCommand(ctx context.Context, command, dir string, timeout time.Duration) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(runCtx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(runCtx, "sh", "-c", command)
	}
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := truncateOutput(out.String(), 64*1024)
	if runCtx.Err() == context.DeadlineExceeded {
		return text, fmt.Errorf("verification timed out after %s", timeout)
	}
	return text, err
}

func truncateOutput(s string, max int) string {
	if len(s) <= max {
		return strings.TrimRight(s, "\n")
	}
	return strings.TrimRight(s[:max], "\n") + "\n... (output truncated)"
}

// registerSubagentTool adds a delegate that runs a fresh, read-only agent.
func registerSubagentTool(r *tools.Registry, router *llm.Router, dir string) {
	readOnly := tools.NewRegistryWithOptions(tools.Options{
		WorkDir:     dir,
		EnableShell: false,
	})
	readOnly.SetPolicy(readOnlyPolicy)
	tool := tools.NewSubagentTool(func(ctx context.Context, task string) (string, error) {
		sub := agent.New(agent.Config{
			LLM:      router,
			Registry: readOnly,
			System:   subagentPrompt,
			MaxSteps: 25,
			Window:   40,
		})
		return sub.Run(ctx, task)
	})
	r.Add(tool)
}

const subagentPrompt = `You are a read-only research subagent for Metis. Investigate the task using
the available file, search, git, web, and repository-map tools. Do not modify
anything: mutating tools and shell commands are unavailable.

Return a focused answer for the parent agent. Include concrete file paths and
line numbers, the commands or sources you checked, and any uncertainty. Do not
repeat the full task; report findings and the recommended next step.`

// readOnlyPolicy allows only tools that cannot change local files or run
// arbitrary commands.
func readOnlyPolicy(name string, _ map[string]any) (bool, string) {
	switch name {
	case "list_dir", "glob", "project_info", "repo_map", "file_read", "file_read_many",
		"file_stat", "search", "hash_file", "json_query", "diff_files",
		"git_status", "git_diff", "git_log", "git_show", "git_blame",
		"http_fetch", "web_search", "system_info", "env_get", "which", "port_check",
		"todo_write", "todo_read", "time":
		return true, ""
	default:
		return false, "the subagent is read-only"
	}
}

// commitFunc returns a function that stages all changes and commits them,
// generating a conventional commit message when none is supplied.
func commitFunc(router *llm.Router, dir string) func(context.Context, string) (string, error) {
	return func(ctx context.Context, message string) (string, error) {
		status, err := gitOutput(ctx, dir, "status", "--porcelain")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(status) == "" {
			return "nothing to commit", nil
		}
		message = strings.TrimSpace(message)
		if message == "" {
			if router == nil {
				return "", fmt.Errorf("no model is available to generate a commit message; pass one explicitly")
			}
			diff, _ := gitOutput(ctx, dir, "diff", "HEAD")
			if strings.TrimSpace(diff) == "" {
				diff = status
			}
			req := []llm.Message{
				{Role: "system", Content: "Write one concise Conventional Commits message for the changes below. " +
					"Use the imperative mood and a scope when obvious. Output only the commit message, with no fences or commentary."},
				{Role: "user", Content: "Status:\n" + status + "\n\nDiff:\n" + truncateOutput(diff, 48*1024)},
			}
			comp, genErr := router.Complete(ctx, req, nil, nil)
			if genErr != nil {
				return "", fmt.Errorf("generate commit message: %w", genErr)
			}
			message = cleanCommitMessage(comp.Content)
			if message == "" {
				message = "chore: update project files"
			}
		}
		if _, err := gitOutput(ctx, dir, "add", "-A"); err != nil {
			return "", err
		}
		out, err := gitOutput(ctx, dir, "commit", "-m", message)
		if err != nil {
			return "", fmt.Errorf("%v\n%s", err, out)
		}
		return "committed: " + firstLine(message) + "\n" + out, nil
	}
}

func cleanCommitMessage(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'`")
	if len(s) > 2000 {
		s = s[:2000]
	}
	return strings.TrimSpace(s)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := strings.TrimRight(out.String(), "\n")
	if err != nil {
		return text, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return text, nil
}
