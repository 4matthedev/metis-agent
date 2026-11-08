package agent

import (
	"strings"

	"metis-agent/internal/tools"
)

// plannerReadOnly is the allowlist used by ModePlanner. Everything not listed
// here is refused and hidden from the model, so Planner stays read-only even
// when auto-approve is enabled. Adding a new tool does not silently widen this
// posture — it must be added here deliberately.
var plannerReadOnly = map[string]struct{}{
	"time":           {},
	"list_dir":       {},
	"glob":           {},
	"project_info":   {},
	"file_read":      {},
	"file_read_many": {},
	"file_stat":      {},
	"search":         {},
	"hash_file":      {},
	"json_query":     {},
	"diff_files":     {},
	"git_status":     {},
	"git_diff":       {},
	"git_log":        {},
	"git_show":       {},
	"git_blame":      {},
	"system_info":    {},
	"env_get":        {},
	"which":          {},
	"port_check":     {},
	"todo_write":     {},
	"todo_read":      {},
	"http_fetch":     {},
	"skills":         {},
}

// argString reads a string argument safely, including from a nil map.
func argString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	s, _ := args[key].(string)
	return s
}

// policyForMode returns the hard tool gate for a mode. A nil policy allows
// every registered tool. Only Planner narrows the toolset today, but the gate
// is intentionally generic so other postures can be tightened later.
func policyForMode(m Mode) tools.Policy {
	if m != ModePlanner {
		return nil
	}
	return func(name string, args map[string]any) (bool, string) {
		// MCP tool names are namespaced as <server>__<tool>; they run on a
		// server Metis does not control and can change external state.
		if strings.Contains(name, "__") {
			return false, "MCP tools can modify external state and are disabled in Planner mode"
		}
		switch name {
		case "skills":
			if strings.EqualFold(strings.TrimSpace(argString(args, "action")), "run") {
				return false, "running a skill executes a command; switch to a non-planning mode to run it"
			}
			return true, ""
		case "http_fetch":
			method := strings.ToUpper(strings.TrimSpace(argString(args, "method")))
			if method == "" {
				method = "GET"
			}
			if method != "GET" && method != "HEAD" {
				return false, "non-GET requests can change remote state and are disabled in Planner mode"
			}
			return true, ""
		}
		if _, ok := plannerReadOnly[name]; !ok {
			return false, "Planner mode is read-only; switch mode to modify the project"
		}
		return true, ""
	}
}
