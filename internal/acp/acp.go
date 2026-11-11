// Package acp implements a pragmatic Agent Client Protocol (ACP) server.
//
// It speaks JSON-RPC 2.0 over stdin/stdout and supports the baseline agent
// methods: initialize, session/new, session/prompt, session/cancel, and the
// session/update notifications. Tool approval is surfaced to the client with
// session/request_permission. Optional ACP features (session/load, client file
// system and terminal methods, MCP servers supplied per session) are not
// implemented yet; the agent uses its own tools instead.
package acp

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"metis-agent/internal/agent"
	"metis-agent/internal/tools"
)

// Factory builds an agent for a session working directory. The returned
// cleanup function releases per-session resources and may be nil.
type Factory func(cwd string) (*agent.Agent, func(), error)

// Options configures the server.
type Options struct {
	// Version is reported to the client as the agent version.
	Version string
	// WorkDir is used when a session omits its cwd.
	WorkDir string
	// Factory creates the agent for a session. It is required.
	Factory Factory
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type response struct {
	result json.RawMessage
	err    *rpcError
}

type session struct {
	id      string
	cwd     string
	agent   *agent.Agent
	cleanup func()

	mu            sync.Mutex
	running       bool
	cancel        context.CancelFunc
	done          chan struct{}
	currentToolID string
	toolIDs       []string
}

func (s *session) pushToolID(id string) {
	s.mu.Lock()
	s.currentToolID = id
	s.toolIDs = append(s.toolIDs, id)
	s.mu.Unlock()
}

func (s *session) popToolID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.toolIDs) == 0 {
		return s.currentToolID
	}
	id := s.toolIDs[0]
	s.toolIDs = s.toolIDs[1:]
	if len(s.toolIDs) > 0 {
		s.currentToolID = s.toolIDs[0]
	}
	return id
}

func (s *session) currentID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentToolID
}

// Server is one ACP connection. Create it with New and run it with Serve.
type Server struct {
	opts Options
	out  io.Writer
	mu   sync.Mutex

	sessions map[string]*session
	pending  map[string]chan response
	nextID   uint64
	writeMu  sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New builds a server for one connection.
func New(opts Options) *Server {
	return &Server{
		opts:     opts,
		sessions: make(map[string]*session),
		pending:  make(map[string]chan response),
	}
}

// Serve reads JSON-RPC messages from in and writes responses and
// notifications to out until in reaches EOF or ctx is cancelled.
func Serve(ctx context.Context, in io.Reader, out io.Writer, opts Options) error {
	s := New(opts)
	return s.Serve(ctx, in, out)
}

// Serve runs the connection on the receiver's options.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	if s.opts.Factory == nil {
		return errors.New("acp: Factory is required")
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.out = out
	defer s.shutdown()

	reader := bufio.NewReaderSize(in, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			s.handleLine(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if s.ctx.Err() != nil {
				return s.ctx.Err()
			}
			return err
		}
	}
}

func (s *Server) handleLine(line []byte) {
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 {
		return
	}
	var msg message
	if err := json.Unmarshal(line, &msg); err != nil {
		s.writeError(nil, -32700, "parse error: "+err.Error())
		return
	}
	if msg.Method == "" {
		s.deliver(msg)
		return
	}
	if len(msg.ID) == 0 || string(msg.ID) == "null" {
		s.handleNotification(msg)
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		result, rerr := s.handleRequest(msg)
		if rerr != nil {
			s.writeError(msg.ID, rerr.Code, rerr.Message)
			return
		}
		s.write(message{JSONRPC: "2.0", ID: msg.ID, Result: result})
	}()
}

func (s *Server) handleNotification(msg message) {
	switch msg.Method {
	case "session/cancel":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return
		}
		if sess := s.session(p.SessionID); sess != nil {
			sess.mu.Lock()
			cancel := sess.cancel
			sess.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
	}
}

func (s *Server) handleRequest(msg message) (json.RawMessage, *rpcError) {
	switch msg.Method {
	case "initialize":
		return s.initialize(msg.Params)
	case "authenticate":
		return mustRaw(map[string]any{}), nil
	case "session/new":
		return s.newSession(msg.Params)
	case "session/prompt":
		return s.prompt(msg.Params)
	case "session/set_mode":
		return s.setMode(msg.Params)
	case "session/close":
		return s.closeSession(msg.Params)
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + msg.Method}
	}
}

func (s *Server) initialize(params json.RawMessage) (json.RawMessage, *rpcError) {
	var p struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	// Version 1 is the only protocol this server implements. If the client
	// speaks a newer major version, reply with ours so it can decide.
	version := 1
	return mustRaw(map[string]any{
		"protocolVersion": version,
		"agentCapabilities": map[string]any{
			"loadSession": false,
			"promptCapabilities": map[string]any{
				"image":           false,
				"audio":           false,
				"embeddedContext": true,
			},
		},
		"agentInfo": map[string]any{
			"name":    "metis-agent",
			"title":   "Metis Agent",
			"version": s.opts.Version,
		},
		"authMethods": []any{},
	}), nil
}

func (s *Server) newSession(params json.RawMessage) (json.RawMessage, *rpcError) {
	var p struct {
		Cwd        string `json:"cwd"`
		MCPServers []any  `json:"mcpServers"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid session/new params: " + err.Error()}
	}
	cwd := strings.TrimSpace(p.Cwd)
	if cwd == "" {
		cwd = s.opts.WorkDir
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, &rpcError{Code: -32603, Message: err.Error()}
		}
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, &rpcError{Code: -32602, Message: "cwd: " + err.Error()}
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return nil, &rpcError{Code: -32602, Message: "cwd is not a directory: " + abs}
	}

	ag, cleanup, err := s.opts.Factory(abs)
	if err != nil {
		return nil, &rpcError{Code: -32000, Message: "could not start a session: " + err.Error()}
	}
	sess := &session{id: randomID("sess_"), cwd: abs, agent: ag, cleanup: cleanup}
	ag.SetApprover(s.approver(sess))

	s.mu.Lock()
	s.sessions[sess.id] = sess
	s.mu.Unlock()
	return mustRaw(map[string]any{"sessionId": sess.id}), nil
}

type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	URI      string `json:"uri"`
	Name     string `json:"name"`
	Resource struct {
		URI      string `json:"uri"`
		MimeType string `json:"mimeType"`
		Text     string `json:"text"`
	} `json:"resource"`
}

func (s *Server) prompt(params json.RawMessage) (json.RawMessage, *rpcError) {
	var p struct {
		SessionID string         `json:"sessionId"`
		Prompt    []contentBlock `json:"prompt"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid session/prompt params: " + err.Error()}
	}
	sess := s.session(p.SessionID)
	if sess == nil {
		return nil, &rpcError{Code: -32002, Message: "unknown session: " + p.SessionID}
	}
	text := promptText(p.Prompt)
	if strings.TrimSpace(text) == "" {
		return nil, &rpcError{Code: -32602, Message: "prompt contains no text"}
	}

	sess.mu.Lock()
	if sess.running {
		sess.mu.Unlock()
		return nil, &rpcError{Code: -32000, Message: "the session is already running a prompt"}
	}
	runCtx, cancel := context.WithCancel(s.ctx)
	done := make(chan struct{})
	sess.running = true
	sess.cancel = cancel
	sess.done = done
	sess.mu.Unlock()
	defer func() {
		cancel()
		sess.mu.Lock()
		sess.running = false
		sess.cancel = nil
		sess.done = nil
		sess.mu.Unlock()
		close(done)
	}()

	msgID := randomID("msg_")
	sess.agent.SetEventSink(s.eventSink(sess, msgID))
	_, err := sess.agent.Run(runCtx, text)
	if err != nil {
		if runCtx.Err() != nil || s.ctx.Err() != nil {
			return mustRaw(map[string]any{"stopReason": "cancelled"}), nil
		}
		if errors.Is(err, agent.ErrMaxSteps) {
			return mustRaw(map[string]any{"stopReason": "max_turn_requests"}), nil
		}
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return mustRaw(map[string]any{"stopReason": "end_turn"}), nil
}

func (s *Server) setMode(params json.RawMessage) (json.RawMessage, *rpcError) {
	var p struct {
		SessionID string `json:"sessionId"`
		ModeID    string `json:"modeId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid session/set_mode params: " + err.Error()}
	}
	sess := s.session(p.SessionID)
	if sess == nil {
		return nil, &rpcError{Code: -32002, Message: "unknown session: " + p.SessionID}
	}
	mode, ok := modeFromID(p.ModeID)
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "unknown mode: " + p.ModeID}
	}
	sess.agent.SetMode(mode)
	s.update(sess, map[string]any{
		"sessionUpdate": "current_mode_update",
		"currentModeId": p.ModeID,
	})
	return mustRaw(map[string]any{}), nil
}

func (s *Server) closeSession(params json.RawMessage) (json.RawMessage, *rpcError) {
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid session/close params: " + err.Error()}
	}
	s.mu.Lock()
	sess := s.sessions[p.SessionID]
	delete(s.sessions, p.SessionID)
	s.mu.Unlock()
	if sess != nil {
		sess.mu.Lock()
		cancel := sess.cancel
		done := sess.done
		sess.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		// Wait for an in-flight prompt before releasing the agent's resources.
		if done != nil {
			<-done
		}
		if sess.cleanup != nil {
			sess.cleanup()
		}
	}
	return mustRaw(map[string]any{}), nil
}

func (s *Server) session(id string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func (s *Server) eventSink(sess *session, msgID string) func(agent.Event) {
	toolSeq := 0
	return func(ev agent.Event) {
		switch ev.Kind {
		case agent.EventText:
			if ev.Text == "" {
				return
			}
			s.update(sess, map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"messageId":     msgID,
				"content":       map[string]any{"type": "text", "text": ev.Text},
			})
		case agent.EventToolCall:
			toolSeq++
			id := fmt.Sprintf("call_%d_%d", ev.Step, toolSeq)
			sess.pushToolID(id)
			update := map[string]any{
				"sessionUpdate": "tool_call",
				"toolCallId":    id,
				"title":         toolTitle(ev.Tool, ev.Args),
				"name":          ev.Tool,
				"kind":          toolKind(ev.Tool),
				"status":        "pending",
			}
			if raw := rawJSON(ev.Args); raw != nil {
				update["rawInput"] = raw
			}
			if locs := toolLocations(sess.cwd, ev.Args); len(locs) > 0 {
				update["locations"] = locs
			}
			s.update(sess, update)
		case agent.EventToolResult:
			id := sess.popToolID()
			status := "completed"
			if strings.HasPrefix(strings.TrimSpace(ev.Result), "error:") {
				status = "failed"
			}
			s.update(sess, map[string]any{
				"sessionUpdate": "tool_call_update",
				"toolCallId":    id,
				"status":        status,
				"content": []any{
					map[string]any{
						"type":    "content",
						"content": map[string]any{"type": "text", "text": ev.Result},
					},
				},
				"rawOutput": ev.Result,
			})
		}
	}
}

func (s *Server) approver(sess *session) tools.Approver {
	return func(ctx context.Context, req tools.ApprovalRequest) (tools.ApprovalDecision, error) {
		options := []map[string]any{
			{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
			{"optionId": "allow-always", "name": "Always allow " + req.Tool, "kind": "allow_always"},
			{"optionId": "reject-once", "name": "Reject", "kind": "reject_once"},
		}
		toolID := sess.currentID()
		if toolID == "" {
			toolID = "call_approval"
		}
		toolCall := map[string]any{
			"toolCallId": toolID,
			"title":      req.Tool,
			"kind":       toolKind(req.Tool),
			"status":     "pending",
		}
		content := []any{}
		if req.Summary != "" {
			content = append(content, map[string]any{
				"type":    "content",
				"content": map[string]any{"type": "text", "text": req.Summary},
			})
		}
		if req.Preview != "" {
			content = append(content, map[string]any{
				"type":    "content",
				"content": map[string]any{"type": "text", "text": req.Preview},
			})
		}
		if len(content) > 0 {
			toolCall["content"] = content
		}
		raw, rerr := s.request(ctx, "session/request_permission", map[string]any{
			"sessionId": sess.id,
			"toolCall":  toolCall,
			"options":   options,
		})
		if rerr != nil {
			if ctx.Err() != nil {
				return tools.ApprovalDeny, ctx.Err()
			}
			return tools.ApprovalDeny, nil
		}
		var out struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return tools.ApprovalDeny, nil
		}
		if out.Outcome.Outcome != "selected" {
			return tools.ApprovalDeny, nil
		}
		switch out.Outcome.OptionID {
		case "allow-once":
			return tools.ApprovalOnce, nil
		case "allow-always":
			return tools.ApprovalAlways, nil
		default:
			return tools.ApprovalDeny, nil
		}
	}
}

func (s *Server) request(ctx context.Context, method string, params any) (json.RawMessage, *rpcError) {
	id := atomic.AddUint64(&s.nextID, 1)
	key := strconv.FormatUint(id, 10)
	ch := make(chan response, 1)
	s.mu.Lock()
	s.pending[key] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, key)
		s.mu.Unlock()
	}()

	if err := s.write(message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(key),
		Method:  method,
		Params:  mustRaw(params),
	}); err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	select {
	case <-ctx.Done():
		return nil, &rpcError{Code: -32800, Message: "cancelled"}
	case resp := <-ch:
		if resp.err != nil {
			return nil, resp.err
		}
		return resp.result, nil
	}
}

func (s *Server) deliver(msg message) {
	key := idKey(msg.ID)
	if key == "" {
		return
	}
	s.mu.Lock()
	ch := s.pending[key]
	s.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- response{result: msg.Result, err: msg.Error}:
	default:
	}
}

func (s *Server) update(sess *session, update map[string]any) {
	s.write(message{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params:  mustRaw(map[string]any{"sessionId": sess.id, "update": update}),
	})
}

func (s *Server) write(msg message) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.out.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func (s *Server) writeError(id json.RawMessage, code int, text string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	_ = s.write(message{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: text}})
}

func (s *Server) shutdown() {
	s.cancel()
	s.mu.Lock()
	sessions := make([]*session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.sessions = map[string]*session{}
	s.mu.Unlock()
	for _, sess := range sessions {
		sess.mu.Lock()
		if sess.cancel != nil {
			sess.cancel()
		}
		sess.mu.Unlock()
	}
	s.wg.Wait()
	for _, sess := range sessions {
		if sess.cleanup != nil {
			sess.cleanup()
		}
	}
}

func promptText(blocks []contentBlock) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		case "resource":
			if b.Resource.Text != "" {
				label := b.Resource.URI
				if label == "" {
					label = "embedded resource"
				}
				parts = append(parts, fmt.Sprintf("<file uri=%q>\n%s\n</file>", label, b.Resource.Text))
			}
		case "resource_link":
			if b.URI != "" {
				name := b.Name
				if name == "" {
					name = b.URI
				}
				parts = append(parts, fmt.Sprintf("(resource: %s — %s)", name, b.URI))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func modeFromID(id string) (agent.Mode, bool) {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "general", "0":
		return agent.ModeGeneral, true
	case "planner", "1":
		return agent.ModePlanner, true
	case "builder", "2":
		return agent.ModeBuilder, true
	case "debugger", "3":
		return agent.ModeDebugger, true
	case "cybersecurity", "4":
		return agent.ModeCybersecurity, true
	case "simplifier", "5":
		return agent.ModeSimplifier, true
	default:
		return agent.ModeGeneral, false
	}
}

func toolKind(name string) string {
	switch name {
	case "file_read", "file_read_many", "file_stat", "list_dir", "project_info",
		"repo_map", "json_query", "hash_file", "git_status", "git_diff",
		"git_log", "git_show", "git_blame", "skills", "time", "system_info",
		"env_get", "which", "port_check":
		return "read"
	case "file_edit", "multi_edit", "replace_in_files", "file_write", "make_dir":
		return "edit"
	case "file_delete":
		return "delete"
	case "file_move":
		return "move"
	case "file_copy":
		return "edit"
	case "search", "glob", "web_search":
		return "search"
	case "shell", "diagnostics":
		return "execute"
	case "http_fetch", "download_file":
		return "fetch"
	case "subagent", "todo_write", "todo_read":
		return "think"
	default:
		return "other"
	}
}

func toolTitle(name, args string) string {
	var obj map[string]any
	if json.Unmarshal([]byte(args), &obj) == nil {
		for _, key := range []string{"path", "pattern", "query", "command", "url", "from", "name"} {
			if v, ok := obj[key].(string); ok && strings.TrimSpace(v) != "" {
				return name + " " + oneLine(v, 120)
			}
		}
	}
	return name
}

func toolLocations(cwd, args string) []map[string]any {
	var obj map[string]any
	if json.Unmarshal([]byte(args), &obj) != nil {
		return nil
	}
	var out []map[string]any
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		p := raw
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		out = append(out, map[string]any{"path": filepath.Clean(p)})
	}
	for _, key := range []string{"path", "from", "to"} {
		if v, ok := obj[key].(string); ok {
			add(v)
		}
	}
	if list, ok := obj["paths"].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok {
				add(s)
			}
		}
	}
	return out
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

func rawJSON(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	return v
}

func mustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

func idKey(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	if unquoted, err := strconv.Unquote(s); err == nil {
		return unquoted
	}
	return s
}

func randomID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + strconv.FormatInt(int64(os.Getpid()), 36)
	}
	return prefix + hex.EncodeToString(b[:])
}
