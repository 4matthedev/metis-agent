package mcp

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func textC(s string) mcpsdk.Content { return &mcpsdk.TextContent{Text: s} }
func imgC(n int) mcpsdk.Content     { return &mcpsdk.ImageContent{Data: make([]byte, n)} }

func TestDefaultServerName(t *testing.T) {
	// Explicit name is preserved.
	if name := DefaultServerName(ServerConfig{Name: "github"}); name != "github" {
		t.Fatalf("explicit name = %q, want github", name)
	}
	// stdio: derived from the command path.
	if name := DefaultServerName(ServerConfig{Command: "/usr/bin/nc"}); !strings.HasSuffix(name, "nc") {
		t.Fatalf("stdio name = %q, want host/path from command", name)
	}
	// http: derived from the URL host (Type must be http); dots are sanitized.
	if name := DefaultServerName(ServerConfig{Type: "http", URL: "https://api.example.com/tools"}); !strings.Contains(name, "example_com") {
		t.Fatalf("http name = %q, want sanitized host", name)
	}
	// stdio with no command -> fallback name (not empty).
	if name := DefaultServerName(ServerConfig{Type: "stdio"}); name == "" {
		t.Fatal("stdio with no command returned empty name")
	}
	// empty config falls back to a name.
	if name := DefaultServerName(ServerConfig{}); name == "" {
		t.Fatal("empty config returned empty name")
	}
}

func TestSanitize(t *testing.T) {
	// sanitize keeps [a-zA-Z0-9_-] and replaces every other run with "_".
	// It also strips a leading "_"/"-" and truncates to maxLen.
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"my.tool", 32, "my_tool"},
		{"path/to/tool", 32, "path_to_tool"},
		{"tool with space", 32, "tool_with_space"},
		{"UPPER_TOOL", 32, "UPPER_TOOL"},
		{"_leading", 32, "leading"},
		{"-leading", 32, "leading"},
		{"abc", 2, "ab"},
		{"", 32, "x"},
	}
	for _, c := range cases {
		got := sanitize(c.in, c.max)
		if got != c.want {
			t.Errorf("sanitize(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestClientNamespacedAndServerName(t *testing.T) {
	c := &Client{serverName: "github"}
	if got := c.Namespaced("create_issue"); got != "github__create_issue" {
		t.Fatalf("Namespaced(create_issue) = %q", got)
	}
	if got := c.Namespaced("tool.name"); got != "github__tool_name" {
		t.Fatalf("Namespaced(tool.name) = %q", got)
	}
	if c.ServerName() != "github" {
		t.Fatalf("ServerName = %q", c.ServerName())
	}
}

func TestResultToText(t *testing.T) {
	// No content, no structured content -> "(no content)".
	if got := resultToText(&mcpsdk.CallToolResult{}); got != "(no content)" {
		t.Fatalf("empty result = %q, want (no content)", got)
	}

	// Single text content.
	got := resultToText(&mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{textC("hello world")},
	})
	if got != "hello world" {
		t.Fatalf("text result = %q", got)
	}

	// Structured content only (no Content) -> marshaled JSON.
	got = resultToText(&mcpsdk.CallToolResult{
		StructuredContent: map[string]int{"a": 1},
	})
	if !strings.Contains(got, `"a":1`) {
		t.Fatalf("structured result = %q", got)
	}

	// Both Content and StructuredContent -> Content wins (no "(no content)").
	got = resultToText(&mcpsdk.CallToolResult{
		Content:           []mcpsdk.Content{textC("x")},
		StructuredContent: map[string]int{"a": 1},
	})
	if got != "x" {
		t.Fatalf("content+structured = %q", got)
	}

	// Image content -> placeholder.
	got = resultToText(&mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{imgC(2048)},
	})
	if !strings.HasPrefix(got, "[image, 2.0 KB]") {
		t.Fatalf("image placeholder = %q", got)
	}
}

func TestSizeText(t *testing.T) {
	if got := sizeText(512); got != "512 B" {
		t.Fatalf("sizeText(512) = %q", got)
	}
	if got := sizeText(2048); got != "2.0 KB" {
		t.Fatalf("sizeText(2048) = %q", got)
	}
	if got := sizeText(2 * 1024 * 1024); got != "2.0 MB" {
		t.Fatalf("sizeText(2MiB) = %q", got)
	}
}

func TestNormalizeSchema(t *testing.T) {
	// nil -> default object schema.
	s := normalizeSchema(nil)
	if s["type"] != "object" {
		t.Fatalf("normalizeSchema(nil) = %v", s)
	}
	// already a map -> returned as-is.
	m := map[string]any{"type": "string"}
	if s := normalizeSchema(m); s["type"] != "string" {
		t.Fatalf("normalizeSchema(map) = %v", s)
	}
	// something else -> marshaled then unmarshaled into a map.
	s = normalizeSchema([]int{1, 2, 3})
	if s == nil {
		t.Fatal("normalizeSchema(slice) = nil")
	}
}

// TestSmokeStdio is a real end-to-end integration test: it builds the tiny MCP
// stdio server in cmd/mcp-smoke-server, connects to it via Connect, lists its
// tools, and calls one. This exercises the process spawn, JSON-RPC
// round-trip, and result flattening.
func TestSmokeStdio(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "smoke-server")
	buildErrOut := &bytes.Buffer{}
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/mcp-smoke-server")
	cmd.Stderr = buildErrOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build smoke server: %v\n%s", err, buildErrOut.String())
	}

	ctx := context.Background()
	c, err := Connect(ctx, ServerConfig{Command: bin})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() {
		if e := c.Close(); e != nil {
			t.Logf("Close: %v", e)
		}
	}()

	specs, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make(map[string]bool)
	for _, s := range specs {
		names[s.Name] = true
		t.Logf("mcp tool: %q desc=%q", s.Name, s.Description)
	}
	if !names["greet"] {
		t.Fatalf("expected greet in tool list; got %v", names)
	}

	// Call a happy-path tool.
	out, err := c.Call(ctx, "greet", map[string]any{"name": "world"})
	if err != nil {
		t.Fatalf("Call(greet): %v", err)
	}
	if !strings.Contains(out, "Hi world") {
		t.Fatalf("Call(greet) = %q, want Hi world", out)
	}

	// Call the erroring tool -> a handler error surfaces as an error from Call.
	out, err = c.Call(ctx, "fail", nil)
	if err == nil {
		t.Fatalf("Call(fail) should return an error, got out=%q", out)
	}
	t.Logf("fail tool error (expected): %v", err)
	if out != "" {
		t.Fatalf("Call(fail) out should be empty for a handler error, got %q", out)
	}
}
