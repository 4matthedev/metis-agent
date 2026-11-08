package tools

import (
	"context"
	"encoding/json"

	"metis-agent/internal/skills"
)

// NewSkillsTool registers the `skills` tool, which lets the agent discover and
// manage skills. Actions:
//
//	{"action":"list"}               -> list skills
//	{"action":"info","name":"x"}   -> show a skill's full text
//	{"action":"use","name":"x"}    -> load a skill into context (agent consults it)
//	{"action":"run","name":"x"}    -> run the skill's declared command, approval-gated
//
// The `run` action is routed through the shell tool's Execute so the same
// approval gate (needApproval + approver) applies — no bypass, no extra
// approval logic.
func NewSkillsTool(manager *skills.Manager, registry *Registry) *Tool {
	props := map[string]any{
		"action": propSchema{Type: "string", Description: "One of: list, info, use, run."},
		"name":   propSchema{Type: "string", Description: "Skill name (for info/use/run)."},
	}
	return &Tool{
		Name: "skills",
		Description: "Manage the agent's skill system. A skill is a SKILL.md file " +
			"(name + description + optional 'run' command + markdown body) found in " +
			"<project>/.metis-agent/skills/, $METIS_SKILL_PATHS, or ~/.agents/skills/. " +
			"Actions: list, info <name>, use <name> (load its body into context), " +
			"run <name> (execute its declared command — always approval-gated, like shell). " +
			"When stuck on a task, first call list and info to see if a skill exists, " +
			"then use it or run its command.",
		Parameters: objectSchema(props, "action"),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			if manager == nil {
				return "error: skill manager is not available", nil
			}
			action := str(args["action"])
			name := str(args["name"])
			if action == "run" {
				cmd, ok := manager.RunCommand(ctx, []string{name})
				if !ok {
					return cmd, nil
				}
				if registry != nil {
					shellInput, _ := json.Marshal(map[string]any{"command": cmd})
					return registry.Execute(ctx, "shell", shellInput), nil
				}
				return cmd, nil
			}
			fields := []string{"skills", action}
			if name != "" {
				fields = append(fields, name)
			}
			return manager.Command(ctx, fields), nil
		},
	}
}
