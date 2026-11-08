package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"runtime"
	"strings"
	"testing"

	"metis-agent/internal/agent"
	"metis-agent/internal/llm"
	"metis-agent/internal/tools"
)

type fakeCompleter struct {
	responses []*llm.Completion
	calls     int
}

func (f *fakeCompleter) Complete(_ context.Context, _ []llm.Message, _ []llm.Tool, onToken func(string)) (*llm.Completion, error) {
	i := f.calls
	f.calls++
	if i >= len(f.responses) {
		return &llm.Completion{Content: "done"}, nil
	}
	r := f.responses[i]
	if onToken != nil && r.Content != "" {
		onToken(r.Content)
	}
	return r, nil
}

func newTestFactory(t *testing.T, fc *fakeCompleter) Factory {
	t.Helper()
	return func(cwd string) (*agent.Agent, func(), error) {
		reg := tools.NewRegistryWithOptions(tools.Options{
			WorkDir:     cwd,
			EnableShell: true,
			AutoApprove: false,
		})
		a := agent.New(agent.Config{
			LLM:      fc,
			Registry: reg,
			System:   "test",
			MaxSteps: 5,
			Window:   32,
		})
		return a, func() {}, nil
	}
}

type testClient struct {
	t      *testing.T
	in     io.Writer
	out    *bufio.Scanner
	server *Server
	done   chan error
}

func startServer(t *testing.T, fc *fakeCompleter) *testClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &testClient{
		t:      t,
		in:     inW,
		out:    bufio.NewScanner(outR),
		server: New(Options{Version: "test", WorkDir: t.TempDir(), Factory: newTestFactory(t, fc)}),
		done:   make(chan error, 1),
	}
	go func() {
		c.done <- c.server.Serve(context.Background(), inR, outW)
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		_ = outR.Close()
	})
	return c
}

func (c *testClient) send(v any) {
	c.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.in.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
}

func (c *testClient) recv() map[string]any {
	c.t.Helper()
	if !c.out.Scan() {
		c.t.Fatalf("no message from server: %v", c.out.Err())
	}
	var msg map[string]any
	if err := json.Unmarshal(c.out.Bytes(), &msg); err != nil {
		c.t.Fatalf("bad server message %q: %v", c.out.Text(), err)
	}
	return msg
}

func (c *testClient) waitResponse(id float64) map[string]any {
	c.t.Helper()
	for {
		msg := c.recv()
		if got, ok := msg["id"].(float64); ok && got == id {
			return msg
		}
	}
}

func initialize(t *testing.T, c *testClient) {
	t.Helper()
	c.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": 1},
	})
	resp := c.waitResponse(1)
	result, _ := resp["result"].(map[string]any)
	if result["protocolVersion"] != float64(1) {
		t.Fatalf("protocolVersion = %v", result["protocolVersion"])
	}
	info, _ := result["agentInfo"].(map[string]any)
	if info["name"] != "metis-agent" {
		t.Fatalf("agentInfo = %#v", info)
	}
}

func newSession(t *testing.T, c *testClient, cwd string) string {
	t.Helper()
	c.send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "session/new",
		"params": map[string]any{"cwd": cwd, "mcpServers": []any{}},
	})
	resp := c.waitResponse(2)
	if e := resp["error"]; e != nil {
		t.Fatalf("session/new failed: %#v", e)
	}
	result, _ := resp["result"].(map[string]any)
	id, _ := result["sessionId"].(string)
	if id == "" {
		t.Fatalf("no sessionId in %#v", result)
	}
	return id
}

func TestInitializeAndPrompt(t *testing.T) {
	fc := &fakeCompleter{responses: []*llm.Completion{{Content: "hello"}}}
	c := startServer(t, fc)
	initialize(t, c)
	sid := newSession(t, c, t.TempDir())

	c.send(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "session/prompt",
		"params": map[string]any{
			"sessionId": sid,
			"prompt":    []any{map[string]any{"type": "text", "text": "say hi"}},
		},
	})

	sawText := false
	var stop string
	for {
		msg := c.recv()
		if msg["method"] == "session/update" {
			params, _ := msg["params"].(map[string]any)
			update, _ := params["update"].(map[string]any)
			if update["sessionUpdate"] == "agent_message_chunk" {
				content, _ := update["content"].(map[string]any)
				if content["text"] == "hello" {
					sawText = true
				}
			}
			continue
		}
		if id, ok := msg["id"].(float64); ok && id == 3 {
			result, _ := msg["result"].(map[string]any)
			stop, _ = result["stopReason"].(string)
			break
		}
	}
	if !sawText {
		t.Fatal("no agent_message_chunk with the model text")
	}
	if stop != "end_turn" {
		t.Fatalf("stopReason = %q", stop)
	}
}

func TestPromptRequestsPermission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell command")
	}
	fc := &fakeCompleter{responses: []*llm.Completion{
		{
			Content:   "checking",
			ToolCalls: []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.ToolFunctionWire{Name: "shell", Arguments: `{"command":"printf hi"}`}}},
		},
		{Content: "done"},
	}}
	c := startServer(t, fc)
	initialize(t, c)
	sid := newSession(t, c, t.TempDir())

	c.send(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "session/prompt",
		"params": map[string]any{
			"sessionId": sid,
			"prompt":    []any{map[string]any{"type": "text", "text": "run it"}},
		},
	})

	sawCall, sawApproval, sawCompleted, sawDone := false, false, false, false
	for {
		msg := c.recv()
		if msg["method"] == "session/request_permission" {
			sawApproval = true
			params, _ := msg["params"].(map[string]any)
			opts, _ := params["options"].([]any)
			if len(opts) == 0 {
				t.Fatal("permission request has no options")
			}
			c.send(map[string]any{
				"jsonrpc": "2.0", "id": msg["id"],
				"result": map[string]any{"outcome": map[string]any{
					"outcome": "selected", "optionId": "allow-once",
				}},
			})
			continue
		}
		if msg["method"] == "session/update" {
			params, _ := msg["params"].(map[string]any)
			update, _ := params["update"].(map[string]any)
			switch update["sessionUpdate"] {
			case "tool_call":
				if update["name"] == "shell" {
					sawCall = true
				}
			case "tool_call_update":
				if update["status"] == "completed" {
					sawCompleted = true
				}
			case "agent_message_chunk":
				content, _ := update["content"].(map[string]any)
				if strings.Contains(content["text"].(string), "done") {
					sawDone = true
				}
			}
			continue
		}
		if id, ok := msg["id"].(float64); ok && id == 3 {
			break
		}
	}
	if !sawCall || !sawApproval || !sawCompleted || !sawDone {
		t.Fatalf("call=%v approval=%v completed=%v done=%v", sawCall, sawApproval, sawCompleted, sawDone)
	}
}

func TestUnknownSession(t *testing.T) {
	fc := &fakeCompleter{}
	c := startServer(t, fc)
	initialize(t, c)
	c.send(map[string]any{
		"jsonrpc": "2.0", "id": 9, "method": "session/prompt",
		"params": map[string]any{
			"sessionId": "sess_missing",
			"prompt":    []any{map[string]any{"type": "text", "text": "hi"}},
		},
	})
	resp := c.waitResponse(9)
	rerr, _ := resp["error"].(map[string]any)
	if rerr == nil || rerr["code"] != float64(-32002) {
		t.Fatalf("error = %#v", resp["error"])
	}
}
