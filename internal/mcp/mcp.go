// Package mcp connects metis to MCP (Model Context Protocol) servers and exposes
// their tools to the agent. Servers can be local stdio processes (spawned by
// command) or remote over streamable HTTP / SSE. All MCP tools are treated as
// "external": they run on a server that metis does not control, so every call
// is subject to the approval gate (unless auto-approve is on).
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServerConfig describes one MCP server in the user-facing config format.
type ServerConfig struct {
	// Name is a short label used to namespace the server's tools (<name>__<tool>).
	// If empty it is derived from the command (stdio) or the URL host (http).
	Name string `json:"name"`

	// Type is "stdio" (default) or "http".
	Type string `json:"type"`

	// Transport is used only for http: "streamable" (2025-03-26, default) or
	// "sse" (2024-11-05).
	Transport string `json:"transport"`

	// Command is the executable to spawn for stdio servers (e.g. "npx", "python").
	Command string `json:"command"`
	// Args are the arguments passed to Command.
	Args []string `json:"args"`
	// Env are extra environment variables for the stdio process. os.Environ()
	// is inherited and these override/extend it.
	Env map[string]string `json:"env"`

	// URL is the endpoint for http servers.
	URL string `json:"url"`
	// AuthType is "bearer" (adds Authorization: Bearer <AuthToken>).
	AuthType string `json:"auth_type"`
	// AuthToken is the token for AuthType bearer.
	AuthToken string `json:"auth_token"`
	// Headers are additional HTTP headers sent on every request for http servers.
	Headers map[string]string `json:"headers"`
}

// sanitize returns a function-name-safe version of s (alphanumeric, "_", "-"),
// truncated to maxLen. Empty input returns "x".
func sanitize(s string, maxLen int) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteRune('_')
		}
		if b.Len() >= maxLen {
			break
		}
	}
	if b.Len() == 0 {
		return "x"
	}
	name := b.String()
	// function names must not start with - or _
	for len(name) > 0 && (name[0] == '_' || name[0] == '-') {
		name = name[1:]
	}
	if len(name) > maxLen {
		name = name[:maxLen]
	}
	return name
}

// DefaultServerName returns a stable name for a server config. It uses the
// explicit Name if present, else derives one from the command (stdio) or the
// URL host (http). Exported so config can merge servers by name.
func DefaultServerName(cfg ServerConfig) string {
	if n := strings.TrimSpace(cfg.Name); n != "" {
		return n
	}
	if cfg.Type == "stdio" || cfg.Type == "" {
		if c := strings.TrimSpace(cfg.Command); c != "" {
			return sanitize(c, 32)
		}
	} else if u := strings.TrimSpace(cfg.URL); u != "" {
		// take the host, drop scheme, strip port
		var host string
		if i := strings.Index(u, "://"); i >= 0 {
			host = u[i+3:]
		} else {
			host = u
		}
		if i := strings.Index(host, "/"); i >= 0 {
			host = host[:i]
		}
		if i := strings.Index(host, ":"); i >= 0 {
			host = host[:i]
		}
		return sanitize(host, 32)
	}
	return sanitize("mcp", 32)
}

// ToolSpec is a transport-neutral description of one MCP tool.
type ToolSpec struct {
	// Name is the name the server exposes (used when calling the tool).
	Name string
	// Description is the human-readable tool description.
	Description string
	// Parameters is the JSON Schema of the tool's arguments.
	Parameters map[string]any
}

// Client is a live connection to one MCP server.
type Client struct {
	serverName string
	session    *mcpsdk.ClientSession
}

// Connect returns a client from a ServerConfig. The caller must Close it when
// done. Returns nil client and an error on failure.
func Connect(ctx context.Context, cfg ServerConfig) (*Client, error) {
	srv := &Client{serverName: sanitize(DefaultServerName(cfg), 32)}

	var transport mcpsdk.Transport

	switch t := strings.ToLower(strings.TrimSpace(cfg.Type)); t {
	case "", "stdio":
		if cfg.Command == "" {
			return nil, fmt.Errorf("stdio server %q requires a command", cfg.Name)
		}
		cmd := exec.Command(cfg.Command, cfg.Args...)
		// build env: base + overrides. Stdout/stderr are left unset so the SDK
		// can use StdoutPipe/StdinPipe (which require them nil).
		env := os.Environ()
		for k, v := range cfg.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = env
		transport = &mcpsdk.CommandTransport{Command: cmd}
	case "http":
		url := strings.TrimSpace(cfg.URL)
		if url == "" {
			return nil, fmt.Errorf("http server %q requires a url", cfg.Name)
		}
		hc := buildHTTPClient(cfg)
		switch tr := strings.ToLower(strings.TrimSpace(cfg.Transport)); tr {
		case "", "streamable":
			transport = &mcpsdk.StreamableClientTransport{Endpoint: url, HTTPClient: hc}
		case "sse":
			transport = &mcpsdk.SSEClientTransport{Endpoint: url, HTTPClient: hc}
		default:
			return nil, fmt.Errorf("unknown http transport %q (want streamable or sse)", tr)
		}
	default:
		return nil, fmt.Errorf("unknown mcp server type %q (want stdio or http)", t)
	}

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "metis", Version: "v1.0.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	srv.session = session
	return srv, nil
}

// buildHTTPClient returns an http.Client. If the config carries auth or
// headers, it wraps a default transport so that every request carries them.
func buildHTTPClient(cfg ServerConfig) *http.Client {
	base := &http.Transport{}
	if cfg.AuthType != "" || len(cfg.Headers) > 0 {
		return &http.Client{
			Timeout:   10 * time.Minute,
			Transport: &headerTransport{t: base, cfg: cfg},
		}
	}
	return &http.Client{
		Timeout:   10 * time.Minute,
		Transport: base,
	}
}

// headerTransport injects static headers (and bearer auth) into every request.
type headerTransport struct {
	t   *http.Transport
	cfg ServerConfig
}

func (h *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if h.cfg.AuthType == "bearer" && h.cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.cfg.AuthToken)
	}
	for k, v := range h.cfg.Headers {
		// skip the Authorization header; it is already set above.
		if strings.EqualFold(k, "Authorization") {
			continue
		}
		req.Header.Set(k, v)
	}
	return h.t.RoundTrip(req)
}

// ListTools returns the server's tools as metis-friendly specs.
func (c *Client) ListTools(ctx context.Context) ([]ToolSpec, error) {
	res, err := c.session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]ToolSpec, 0, len(res.Tools))
	for _, t := range res.Tools {
		out = append(out, ToolSpec{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  normalizeSchema(t.InputSchema),
		})
	}
	return out, nil
}

// Call invokes one MCP tool. toolName is the name as the server exposes it.
// args is the parsed argument object.
func (c *Client) Call(ctx context.Context, toolName string, args map[string]any) (string, error) {
	params := mcpsdk.CallToolParams{
		Name:      toolName,
		Arguments: map[string]any{},
	}
	if args != nil {
		params.Arguments = args
	}
	res, err := c.session.CallTool(ctx, &params)
	if err != nil {
		return "", err
	}
	if res == nil {
		return "(no content)", nil
	}
	text := resultToText(res)
	if res.IsError {
		return "error: " + text, nil
	}
	return text, nil
}

// Close disconnects and (for stdio) shuts down the server process.
func (c *Client) Close() error {
	if c.session == nil {
		return nil
	}
	return c.session.Close()
}

// ServerName returns the sanitized display/namespace name.
func (c *Client) ServerName() string {
	return c.serverName
}

// Namespaced returns the metis function name for a tool: <server>__<tool>.
func (c *Client) Namespaced(toolName string) string {
	srv := sanitize(c.serverName, 32)
	tl := sanitize(toolName, 32)
	full := srv + "__" + tl
	// truncate to a safe length for OpenAI function names.
	const maxLen = 64
	if len(full) > maxLen {
		full = full[:maxLen]
	}
	return full
}

// normalizeSchema ensures the input schema is a map[string]any (what the model
// expects in "parameters").
func normalizeSchema(s any) map[string]any {
	if m, ok := s.(map[string]any); ok {
		return m
	}
	if s == nil {
		return map[string]any{"type": "object"}
	}
	var m map[string]any
	if b, err := json.Marshal(s); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	if m == nil {
		m = map[string]any{"type": "object"}
	}
	return m
}

// resultToText flattens a CallToolResult into a single string for the model.
func resultToText(res *mcpsdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		switch t := c.(type) {
		case *mcpsdk.TextContent:
			b.WriteString(t.Text)
		case *mcpsdk.ImageContent:
			b.WriteString("[image, " + sizeText(len(t.Data)) + "]")
		case *mcpsdk.AudioContent:
			b.WriteString("[audio, " + sizeText(len(t.Data)) + "]")
		default:
			if j, err := json.Marshal(c); err == nil {
				b.Write(j)
			}
		}
	}
	if res.StructuredContent != nil && strings.TrimSpace(b.String()) == "" {
		if j, err := json.Marshal(res.StructuredContent); err == nil {
			b.WriteString(string(j))
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		b.WriteString("(no content)")
	}
	return b.String()
}

func sizeText(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
