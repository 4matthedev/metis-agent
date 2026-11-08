package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type todoItem struct {
	Text   string
	Status string
}

type todoStore struct {
	mu    sync.Mutex
	items []todoItem
}

func newTodoStore() *todoStore { return &todoStore{} }

func (s *todoStore) snapshot() []todoItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]todoItem, len(s.items))
	copy(out, s.items)
	return out
}

func (s *todoStore) replace(items []todoItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = items
}

// NewTodoWriteTool replaces the agent's task list.
func NewTodoWriteTool(store *todoStore) *Tool {
	itemSchema := objectSchema(map[string]any{
		"text":   propSchema{Type: "string", Description: "Short description of the step."},
		"status": propSchema{Type: "string", Description: "pending, in_progress, or completed (default pending)."},
	}, "text")
	return &Tool{
		Name: "todo_write",
		Description: "Replace the task list for the current work. Use it to plan multi-step tasks and keep progress visible. " +
			"At most one item may be in_progress. Write the full list each time to update statuses.",
		Parameters: objectSchema(map[string]any{
			"items": map[string]any{
				"type":        "array",
				"description": "The complete task list. Pass an empty array to clear it.",
				"items":       itemSchema,
			},
		}, "items"),
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			raw, ok := args["items"].([]any)
			if !ok {
				return "", fmt.Errorf("items must be an array")
			}
			if len(raw) > 100 {
				return "", fmt.Errorf("too many items (%d); keep the list under 100", len(raw))
			}
			items := make([]todoItem, 0, len(raw))
			inProgress := 0
			for i, entry := range raw {
				m, ok := entry.(map[string]any)
				if !ok {
					return "", fmt.Errorf("item %d must be an object", i+1)
				}
				text := strings.TrimSpace(str(m["text"]))
				if text == "" {
					return "", fmt.Errorf("item %d: text is required", i+1)
				}
				if len(text) > 500 {
					return "", fmt.Errorf("item %d: text is too long (max 500 characters)", i+1)
				}
				status := strings.ToLower(strings.TrimSpace(str(m["status"])))
				switch status {
				case "":
					status = "pending"
				case "pending", "in_progress", "completed":
				case "in-progress", "active", "doing":
					status = "in_progress"
				case "done", "complete":
					status = "completed"
				default:
					return "", fmt.Errorf("item %d: invalid status %q (use pending, in_progress, or completed)", i+1, status)
				}
				if status == "in_progress" {
					inProgress++
				}
				items = append(items, todoItem{Text: text, Status: status})
			}
			if inProgress > 1 {
				return "", fmt.Errorf("at most one item may be in_progress (found %d)", inProgress)
			}
			store.replace(items)
			if len(items) == 0 {
				return "todo list cleared", nil
			}
			return formatTodoList(items), nil
		},
	}
}

// NewTodoReadTool returns the current task list.
func NewTodoReadTool(store *todoStore) *Tool {
	return &Tool{
		Name:        "todo_read",
		Description: "Read the current task list, including pending, in-progress, and completed items.",
		Parameters:  objectSchema(map[string]any{}),
		Handler: func(_ context.Context, _ map[string]any) (string, error) {
			items := store.snapshot()
			if len(items) == 0 {
				return "todo list is empty", nil
			}
			return formatTodoList(items), nil
		},
	}
}

func formatTodoList(items []todoItem) string {
	done := 0
	for _, item := range items {
		if item.Status == "completed" {
			done++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "todo list: %d item(s), %d completed\n", len(items), done)
	for _, item := range items {
		mark := "[ ]"
		switch item.Status {
		case "completed":
			mark = "[x]"
		case "in_progress":
			mark = "[~]"
		}
		fmt.Fprintf(&b, "%s %s\n", mark, item.Text)
	}
	return strings.TrimRight(b.String(), "\n")
}
