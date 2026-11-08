package tools

import (
	"context"
	"fmt"
	"strings"
)

// SubagentRunner runs one isolated task and returns its answer.
type SubagentRunner func(ctx context.Context, task string) (string, error)

// NewSubagentTool exposes a delegate to a fresh, typically read-only agent.
func NewSubagentTool(run SubagentRunner) *Tool {
	return &Tool{
		Name: "subagent",
		Description: "Delegate an independent investigation to a fresh subagent with its own context. " +
			"The subagent can read files, search, use git, and search the web, but cannot change anything. " +
			"Use it for broad exploration so the main conversation stays focused.",
		Parameters: objectSchema(map[string]any{
			"task": propSchema{Type: "string", Description: "A self-contained question or investigation with the context the subagent needs."},
		}, "task"),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			task := strings.TrimSpace(str(args["task"]))
			if task == "" {
				return "", fmt.Errorf("task is required")
			}
			if run == nil {
				return "", fmt.Errorf("no subagent runner is configured")
			}
			return run(ctx, task)
		},
	}
}
