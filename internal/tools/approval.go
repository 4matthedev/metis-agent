package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// ApprovalDecision is the user's answer to an approval request.
type ApprovalDecision int

const (
	// ApprovalDeny rejects this call.
	ApprovalDeny ApprovalDecision = iota
	// ApprovalOnce allows this call only.
	ApprovalOnce
	// ApprovalAlways allows every future call of the same tool for this session.
	ApprovalAlways
	// ApprovalAll allows every future dangerous call for this session.
	ApprovalAll
)

// Approver asks the user whether a potentially dangerous tool call may run.
// It must block until a decision is available or ctx is cancelled.
type Approver func(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error)

// ApprovalRequest describes one tool call that needs confirmation.
type ApprovalRequest struct {
	Tool    string
	Summary string
	Reason  string
}

// approvalState is embedded in Registry. The mutex protects the fields below.
type approvalState struct {
	mu          sync.RWMutex
	approver    Approver
	autoApprove bool
	always      map[string]bool
}

func (s *approvalState) setApprover(fn Approver) {
	s.mu.Lock()
	s.approver = fn
	s.mu.Unlock()
}

func (s *approvalState) setAutoApprove(v bool) {
	s.mu.Lock()
	s.autoApprove = v
	s.mu.Unlock()
}

func (s *approvalState) isAutoApprove() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.autoApprove
}

func (s *approvalState) allowTool(tool string) {
	s.mu.Lock()
	if s.always == nil {
		s.always = make(map[string]bool)
	}
	s.always[tool] = true
	s.mu.Unlock()
}

// request asks for confirmation unless auto-approval or a per-tool allow rule
// is already active. A nil approver denies the call rather than silently
// allowing a dangerous operation.
func (s *approvalState) request(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error) {
	s.mu.RLock()
	auto := s.autoApprove
	always := s.always[req.Tool]
	approver := s.approver
	s.mu.RUnlock()
	if auto || always {
		return ApprovalOnce, nil
	}
	if approver == nil {
		return ApprovalDeny, fmt.Errorf("approval required for %s, but no interactive approver is available; rerun with --auto-approve to allow it", req.Tool)
	}
	decision, err := approver(ctx, req)
	if err != nil {
		return ApprovalDeny, err
	}
	switch decision {
	case ApprovalAll:
		s.setAutoApprove(true)
	case ApprovalAlways:
		s.allowTool(req.Tool)
	}
	return decision, nil
}

// needApproval classifies a tool call. It returns true for operations that can
// affect files outside the project, delete data, mutate the network, or run
// arbitrary commands.
func (r *Registry) needApproval(name string, args map[string]any) (ApprovalRequest, bool) {
	req := ApprovalRequest{Tool: name}
	// MCP tools run on a server metis does not control; every call is
	// subject to approval (or auto-approve).
	if srv, ok := r.isExternal(name); ok {
		req.Reason = fmt.Sprintf("MCP tool %q on external server %q can read/write files and reach the network", name, srv)
		return req, true
	}
	outside := "accessing paths outside the project can expose or modify unrelated files."
	readOutside := "reading outside the project can expose unrelated files and secrets."
	path := func(key string) string { return str(args[key]) }
	outsideAny := func(values ...string) bool {
		for _, v := range values {
			if strings.TrimSpace(v) != "" && !r.insideProject(v) {
				return true
			}
		}
		return false
	}

	switch name {
	case "shell":
		// Show the command verbatim. Truncating the preview would let a
		// payload hide past the cut, which defeats the approval gate.
		req.Summary = "$ " + path("command")
		req.Reason = "shell commands can modify files, run programs, and access the network."
		return req, true

	case "file_delete":
		req.Summary = path("path")
		if boolArg(args, "recursive") {
			req.Summary += "  (recursive)"
		}
		req.Reason = "deleting files or directories is permanent."
		return req, true

	case "download_file":
		req.Summary = path("url") + "  ->  " + path("path")
		req.Reason = "downloads write data to disk and contact the network."
		return req, true

	case "replace_in_files":
		if !boolArg(args, "dry_run") {
			req.Summary = "/" + path("pattern") + "/ in " + defaultString(path("path"), ".")
			req.Reason = "a bulk replacement changes many files at once."
			return req, true
		}
		if outsideAny(path("path")) {
			req.Summary = path("path")
			req.Reason = readOutside
			return req, true
		}

	case "http_fetch":
		method := strings.ToUpper(defaultString(strings.TrimSpace(path("method")), "GET"))
		if method != "GET" && method != "HEAD" {
			req.Summary = method + " " + path("url")
			req.Reason = "non-GET requests can change remote state."
			return req, true
		}

	case "env_get":
		if boolArg(args, "show_secrets") {
			req.Summary = "show_secrets=true"
			req.Reason = "environment variables often contain credentials."
			return req, true
		}

	case "file_write", "file_edit", "multi_edit", "make_dir":
		if outsideAny(path("path")) {
			req.Summary = path("path")
			req.Reason = outside
			return req, true
		}

	case "file_move", "file_copy":
		if outsideAny(path("from"), path("to")) {
			req.Summary = path("from") + "  ->  " + path("to")
			req.Reason = outside
			return req, true
		}

	case "list_dir", "glob", "file_read", "file_stat", "project_info", "hash_file":
		if outsideAny(path("path")) {
			req.Summary = path("path")
			req.Reason = readOutside
			return req, true
		}

	case "file_read_many":
		paths := stringList(args["paths"])
		for _, p := range paths {
			if !r.insideProject(p) {
				req.Summary = oneLineLimit(strings.Join(paths, ", "), 300)
				req.Reason = readOutside
				return req, true
			}
		}

	case "json_query":
		if p := path("path"); p != "" && !r.insideProject(p) {
			req.Summary = p
			req.Reason = readOutside
			return req, true
		}

	case "diff_files":
		if outsideAny(path("a"), path("b")) {
			req.Summary = path("a") + "  vs  " + path("b")
			req.Reason = readOutside
			return req, true
		}

	case "search":
		if outsideAny(path("path")) {
			req.Summary = "/" + path("pattern") + "/ in " + defaultString(path("path"), ".")
			req.Reason = readOutside
			return req, true
		}

	case "git_status", "git_diff", "git_log", "git_show", "git_blame":
		if outsideAny(path("path"), path("file")) {
			req.Summary = path("path")
			if f := path("file"); f != "" {
				req.Summary = f
			}
			req.Reason = readOutside
			return req, true
		}
	}
	return ApprovalRequest{}, false
}

func oneLineLimit(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// insideProject reports whether p resolves to the project directory or below.
// Symlinks are resolved for the longest existing prefix so a link inside the
// project cannot silently reach outside it. Empty paths are treated as inside
// so malformed calls reach the tool and produce a normal argument error instead
// of an approval prompt.
func (r *Registry) insideProject(p string) bool {
	if strings.TrimSpace(p) == "" {
		return true
	}
	abs, err := resolvePath(r.workDir, p)
	if err != nil {
		return false
	}
	root, err := filepath.Abs(r.workDir)
	if err != nil {
		return false
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	abs = resolveExisting(abs)
	rel, err := filepath.Rel(filepath.Clean(root), abs)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// resolveExisting resolves symlinks in the longest existing prefix of p and
// appends the components that do not exist yet. It returns p unchanged when no
// prefix can be resolved (for example, an unavailable root).
func resolveExisting(p string) string {
	var rest string
	cur := filepath.Clean(p)
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return real
			}
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
