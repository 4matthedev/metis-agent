// Package tools provides Metis's tool registry and approval-gated executors.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"metis-agent/internal/llm"
	"metis-agent/internal/redact"
)

// maxToolResult caps a single tool result before it is appended to the model
// context. Individual tools may return more; the registry truncates the middle.
const maxToolResult = 96 * 1024

// Policy decides whether a tool may run in the agent's current posture. It
// receives the decoded arguments so it can reject a specific action (for
// example, the mutating "run" action of the skills tool). When args is nil the
// caller only needs name-level filtering (see Specs). A false result must
// explain the refusal; that reason is shown to the model.
type Policy func(name string, args map[string]any) (allowed bool, reason string)

// Registry holds the tools the agent may call and executes them by name.
type Registry struct {
	ordered []*Tool
	byName  map[string]*Tool
	workDir string
	// external maps an MCP tool's function name to its source server name.
	// These run on an untrusted server and always need approval.
	external map[string]string
	// policy, when set, removes tools from the model's tool list and refuses
	// direct calls to them. It is a hard gate; approval cannot override it.
	policyMu sync.RWMutex
	policy   Policy
	// mutationMu guards the mutation hook list, the per-run mutation counter,
	// and the redaction switch.
	mutationMu    sync.Mutex
	mutationHooks []MutationHook
	mutations     int
	redact        bool
	approveEdits  bool
	approvalState
}

// MutationHook is called before a tool that can change the filesystem runs.
// paths contains the resolved paths the tool is expected to touch, which lets
// checkpointing snapshot them before the change.
type MutationHook func(name string, args map[string]any, paths []string)

// Options configures which tools are registered.
type Options struct {
	WorkDir      string
	EnableShell  bool
	ShellTimeout time.Duration
	MaxOutput    int
	// AutoApprove allows dangerous calls without asking. Interactive frontends
	// leave this false and install an Approver instead.
	AutoApprove bool
}

// NewRegistry returns a registry with the default toolset. Dangerous calls are
// auto-approved for this legacy constructor; the CLI uses
// NewRegistryWithOptions so it can prompt the user.
func NewRegistry(workDir string) *Registry {
	return NewRegistryWithOptions(Options{WorkDir: workDir, EnableShell: true, AutoApprove: true})
}

// NewRegistryWithOptions returns a registry configured by opts.
func NewRegistryWithOptions(opts Options) *Registry {
	r := &Registry{
		byName:   make(map[string]*Tool),
		workDir:  opts.WorkDir,
		external: make(map[string]string),
		approvalState: approvalState{
			autoApprove: opts.AutoApprove,
			always:      make(map[string]bool),
		},
	}
	todos := newTodoStore()

	// Discovery and reading.
	r.Add(NewListDirTool(opts.WorkDir))
	r.Add(NewGlobTool(opts.WorkDir))
	r.Add(NewProjectInfoTool(opts.WorkDir))
	r.Add(NewFileReadTool(opts.WorkDir))
	r.Add(NewFileReadManyTool(opts.WorkDir))
	r.Add(NewFileStatTool(opts.WorkDir))
	r.Add(NewSearchTool(opts.WorkDir))
	r.Add(NewHashFileTool(opts.WorkDir))
	r.Add(NewJSONQueryTool(opts.WorkDir))

	// Editing and filesystem changes.
	r.Add(NewFileEditTool(opts.WorkDir))
	r.Add(NewMultiEditTool(opts.WorkDir))
	r.Add(NewReplaceInFilesTool(opts.WorkDir))
	r.Add(NewFileWriteTool(opts.WorkDir))
	r.Add(NewMakeDirTool(opts.WorkDir))
	r.Add(NewFileMoveTool(opts.WorkDir))
	r.Add(NewFileCopyTool(opts.WorkDir))
	r.Add(NewFileDeleteTool(opts.WorkDir))
	r.Add(NewDiffFilesTool(opts.WorkDir))

	// Git.
	r.Add(NewGitStatusTool(opts.WorkDir))
	r.Add(NewGitDiffTool(opts.WorkDir))
	r.Add(NewGitLogTool(opts.WorkDir))
	r.Add(NewGitShowTool(opts.WorkDir))
	r.Add(NewGitBlameTool(opts.WorkDir))

	// Web, repository orientation, and system diagnostics.
	r.Add(NewHTTPFetchTool())
	r.Add(NewWebSearchTool())
	r.Add(NewDownloadFileTool(opts.WorkDir))
	r.Add(NewRepoMapTool(opts.WorkDir))
	r.Add(NewDiagnosticsTool(opts.WorkDir))
	r.Add(NewSystemInfoTool(opts.WorkDir))
	r.Add(NewEnvGetTool())
	r.Add(NewWhichTool())
	r.Add(NewPortCheckTool())

	// Planning.
	r.Add(NewTodoWriteTool(todos))
	r.Add(NewTodoReadTool(todos))

	if opts.EnableShell {
		r.Add(NewShellTool(opts.WorkDir, opts.ShellTimeout, opts.MaxOutput))
	}
	r.Add(NewTimeTool())
	return r
}

// SetApprover installs the callback used to confirm dangerous tool calls.
func (r *Registry) SetApprover(fn Approver) { r.approvalState.setApprover(fn) }

// SetAutoApprove enables or disables automatic approval of dangerous calls.
func (r *Registry) SetAutoApprove(v bool) { r.approvalState.setAutoApprove(v) }

// AutoApprove reports whether dangerous calls run without confirmation.
func (r *Registry) AutoApprove() bool { return r.approvalState.isAutoApprove() }

// SetPolicy installs the posture gate. A nil policy allows every registered
// tool. Policies are enforced before approval, so auto-approve cannot bypass
// them.
func (r *Registry) SetPolicy(p Policy) {
	r.policyMu.Lock()
	r.policy = p
	r.policyMu.Unlock()
}

// SetRedactSecrets enables or disables credential redaction on tool output.
func (r *Registry) SetRedactSecrets(v bool) {
	r.mutationMu.Lock()
	r.redact = v
	r.mutationMu.Unlock()
}

// SetApproveEdits controls whether file edits inside the project ask for
// confirmation with a diff preview.
func (r *Registry) SetApproveEdits(v bool) {
	r.mutationMu.Lock()
	r.approveEdits = v
	r.mutationMu.Unlock()
}

func (r *Registry) approveEditsEnabled() bool {
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	return r.approveEdits
}

// AddMutationHook registers a callback that runs before a mutating tool. The
// hook receives the resolved paths that are about to change.
func (r *Registry) AddMutationHook(fn MutationHook) {
	if fn == nil {
		return
	}
	r.mutationMu.Lock()
	r.mutationHooks = append(r.mutationHooks, fn)
	r.mutationMu.Unlock()
}

// Mutations reports how many mutating tool calls have succeeded since the
// counter was last reset.
func (r *Registry) Mutations() int {
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	return r.mutations
}

// ResetMutations zeroes the mutation counter.
func (r *Registry) ResetMutations() {
	r.mutationMu.Lock()
	r.mutations = 0
	r.mutationMu.Unlock()
}

// notifyMutation invokes the hooks and returns whether the call is mutating.
func (r *Registry) notifyMutation(name string, args map[string]any, paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	r.mutationMu.Lock()
	hooks := append([]MutationHook(nil), r.mutationHooks...)
	r.mutationMu.Unlock()
	for _, fn := range hooks {
		fn(name, args, paths)
	}
	return true
}

// allow reports whether the policy permits a tool call. It is safe to call
// with args == nil for name-level filtering.
func (r *Registry) allow(name string, args map[string]any) (bool, string) {
	r.policyMu.RLock()
	p := r.policy
	r.policyMu.RUnlock()
	if p == nil {
		return true, ""
	}
	return p(name, args)
}

// Add registers a tool.
func (r *Registry) Add(t *Tool) {
	if r.byName[t.Name] != nil {
		return
	}
	r.ordered = append(r.ordered, t)
	r.byName[t.Name] = t
}

// SetSkillsTool installs the skills tool (which needs the skills manager and a
// reference to the registry for the approval-gated run action). Call this after
// the registry is built and the skills manager is available.
func (r *Registry) SetSkillsTool(t *Tool) {
	r.Add(t)
}

// RegisterExternalTool registers a tool that runs on an external (MCP) server
// and therefore always requires user approval, even when the name matches a
// built-in pattern.
func (r *Registry) RegisterExternalTool(t *Tool, serverName string) {
	if r.byName[t.Name] != nil {
		return
	}
	r.ordered = append(r.ordered, t)
	r.byName[t.Name] = t
	r.external[t.Name] = serverName
}

// ReloadMCP replaces the MCP (external) tools registered for the given
// serverName. Existing tools that belong to that server (by name prefix) are
// removed; newTools are added in registration order. This lets a manager
// disconnect and reconnect a server without touching the built-in toolset.
// Reloads are expected to happen while the agent is idle.
func (r *Registry) ReloadMCP(serverName string, newTools []Tool) {
	prefix := sanitizeMCPName(serverName, 32) + "__"
	// Remove existing tools for this server.
	var ordered []*Tool
	for _, t := range r.ordered {
		if strings.HasPrefix(t.Name, prefix) {
			continue
		}
		ordered = append(ordered, t)
	}
	for name := range r.byName {
		if strings.HasPrefix(name, prefix) {
			delete(r.byName, name)
		}
	}
	// Add the new tools.
	for _, t := range newTools {
		if r.byName[t.Name] != nil {
			continue
		}
		ordered = append(ordered, &Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
			Handler:     t.Handler,
		})
		r.byName[t.Name] = ordered[len(ordered)-1]
	}
	r.ordered = ordered
}

// sanitizeMCPName mirrors internal/mcp.sanitize so we can compute the namespace
// prefix without importing the mcp package (avoids a cyclic dependency).
// sanitizeMCPName mirrors internal/mcp.sanitize so the prefix can be computed
// here without importing the mcp package (which would create a cycle).
func sanitizeMCPName(s string, maxLen int) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteRune('_')
		}
		if b.Len() >= maxLen {
			break
		}
	}
	name := b.String()
	for len(name) > 0 && (name[0] == '_' || name[0] == '-') {
		name = name[1:]
	}
	if len(name) > maxLen {
		name = name[:maxLen]
	}
	return name
}

// isExternal reports whether a tool runs on an untrusted MCP server.
func (r *Registry) isExternal(name string) (string, bool) {
	s, ok := r.external[name]
	return s, ok
}

// List returns all registered tools in registration order.
func (r *Registry) List() []Tool {
	out := make([]Tool, len(r.ordered))
	for i, t := range r.ordered {
		out[i] = *t
	}
	return out
}

// Specs returns the tools as OpenAI-compatible definitions. Tools rejected by
// the active posture policy are omitted so the model does not see them.
func (r *Registry) Specs() []llm.Tool {
	out := make([]llm.Tool, 0, len(r.ordered))
	for _, t := range r.ordered {
		if ok, _ := r.allow(t.Name, nil); !ok {
			continue
		}
		out = append(out, t.spec())
	}
	return out
}

// Execute runs the tool named name with JSON input. Errors are returned as a
// plain error string so the model can react to them (retry or report). ctx is
// passed to the handler so long-running tools can be interrupted.
func (r *Registry) Execute(ctx context.Context, name string, input []byte) string {
	t, ok := r.byName[name]
	if !ok {
		names := make([]string, len(r.ordered))
		for i, tt := range r.ordered {
			names[i] = tt.Name
		}
		return fmt.Sprintf("error: unknown tool %q. Available tools: %s", name, strings.Join(names, ", "))
	}
	var args map[string]any
	if s := strings.TrimSpace(string(input)); s == "" || s == "{}" {
		args = map[string]any{}
	} else if err := json.Unmarshal(input, &args); err != nil {
		return fmt.Sprintf("error: invalid tool arguments: %v", err)
	}
	if ok, reason := r.allow(name, args); !ok {
		return fmt.Sprintf("error: tool %s is not available: %s", name, reason)
	}
	if req, ok := r.needApproval(name, args); ok {
		decision, err := r.approvalState.request(ctx, req)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		if decision == ApprovalDeny {
			return fmt.Sprintf("error: user denied permission to run %s. Do not retry this call; ask the user how to proceed.", name)
		}
	}
	paths := r.mutationPaths(name, args)
	mutating := r.notifyMutation(name, args, paths)
	out, err := t.Handler(ctx, args)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if mutating {
		r.mutationMu.Lock()
		r.mutations++
		r.mutationMu.Unlock()
	}
	out = truncateText(out, maxToolResult)
	r.mutationMu.Lock()
	shouldRedact := r.redact
	r.mutationMu.Unlock()
	if shouldRedact {
		out = redact.String(out)
	}
	return out
}

// mutationPaths resolves the paths a tool is expected to change. A nil result
// marks the call as read-only.
func (r *Registry) mutationPaths(name string, args map[string]any) []string {
	add := func(dst []string, key string) []string {
		raw := strings.TrimSpace(str(args[key]))
		if raw == "" {
			return dst
		}
		if p, err := resolvePath(r.workDir, raw); err == nil {
			dst = append(dst, p)
		}
		return dst
	}
	var paths []string
	switch name {
	case "file_write", "file_edit", "multi_edit", "make_dir", "file_delete", "download_file":
		paths = add(paths, "path")
	case "file_copy":
		paths = add(paths, "to")
	case "file_move":
		paths = add(paths, "from")
		paths = add(paths, "to")
	case "replace_in_files":
		if !boolArg(args, "dry_run") {
			paths = add(paths, "path")
		}
	}
	return paths
}
