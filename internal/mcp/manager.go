// Package mcp's ServerManager lets an agent (TUI or REPL) manage MCP servers at
// runtime: list them, add them, remove them, reconnect them, and see what
// tools each one exposes. It persists the configured servers to the per-project
// config file (`.metis-agent.json`) so they survive restarts, and it updates
// the tool registry live so new/removed MCP tools are reflected immediately
// (no need to restart the process).
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"metis-agent/internal/tools"
)

// Manager owns MCP server state and applies changes to the tool registry.
// It is driven sequentially when the agent is idle (TUI/REPL slash commands).
type Manager struct {
	servers  map[string]serverEntry
	registry *tools.Registry
	// configPath is the absolute path to the project config file that the
	// manager writes back to. Empty means "do not persist".
	configPath string
}

// serverEntry is the runtime state for one configured MCP server.
type serverEntry struct {
	name   string
	cfg    ServerConfig
	client *Client
	tools  []ToolSpec
}

// NewManager wires a manager to a registry and a project config path.
func NewManager(registry *tools.Registry, configPath string) *Manager {
	return &Manager{
		servers:    make(map[string]serverEntry),
		registry:   registry,
		configPath: configPath,
	}
}

// Load reads any servers already saved to the project config and connects them,
// registering their tools. Failures to connect are recorded and the server is
// still kept in the list so the user can retry.
func (m *Manager) Load(ctx context.Context) {
	for _, cfg := range m.readServers() {
		name := cfg.Name
		if name == "" {
			name = DefaultServerName(cfg)
		}
		cfg.Name = name
		e := serverEntry{name: name, cfg: cfg}
		client, err := Connect(ctx, cfg)
		if err != nil {
			// Keep it listed with no client (status will show not connected).
			m.servers[name] = e
			continue
		}
		specs, lerr := client.ListTools(ctx)
		if lerr == nil {
			e.client = client
			e.tools = specs
		}
		m.servers[name] = e
	}
	m.applyRegistry()
}

// List returns a human-readable listing of all configured MCP servers.
func (m *Manager) List() string {
	if len(m.servers) == 0 {
		return "no MCP servers configured"
	}
	var b strings.Builder
	b.WriteString("mcp servers\n")
	for _, e := range m.sorted() {
		fmt.Fprintf(&b, "  %s  %s  (%d tools)\n", e.name, e.status(), len(e.tools))
	}
	return b.String()
}

// ListDetails returns a longer listing with the tool names of each server.
func (m *Manager) ListDetails() string {
	if len(m.servers) == 0 {
		return "no MCP servers configured"
	}
	var b strings.Builder
	b.WriteString("mcp servers\n")
	for _, e := range m.sorted() {
		fmt.Fprintf(&b, "  %s  %s\n", e.name, e.status())
		if e.client == nil {
			continue
		}
		if len(e.tools) == 0 {
			b.WriteString("    (no tools)\n")
			continue
		}
		for _, spec := range e.tools {
			fmt.Fprintf(&b, "    %s\n", m.serverNamespaced(e.name, spec.Name))
		}
	}
	return b.String()
}

// Add adds (or updates) an MCP server from a JSON config fragment, e.g.:
//
//	{"type":"stdio","command":"npx","args":["-y","@modelcontextprotocol/cli"]}
//	{"type":"http","transport":"streamable","url":"https://mcp.example.com/mcp"}
//
// An optional leading `name` field sets the server name.
func (m *Manager) Add(ctx context.Context, raw string) (string, error) {
	var cfg ServerConfig
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &cfg); err != nil {
		return "", fmt.Errorf("invalid mcp server config: %w", err)
	}
	name := cfg.Name
	if name == "" {
		name = DefaultServerName(cfg)
	}
	cfg.Name = name
	e := serverEntry{name: name, cfg: cfg}
	client, err := Connect(ctx, cfg)
	if err != nil {
		// Keep the entry listed (status: not connected) and warn via caller.
		m.servers[name] = e
		m.saveConfig()
		return name, fmt.Errorf("could not connect to %q: %w", name, err)
	}
	specs, lerr := client.ListTools(ctx)
	e.client = client
	if lerr == nil {
		e.tools = specs
	}
	m.servers[name] = e
	if err := m.saveConfig(); err != nil {
		_ = err
	}
	m.applyRegistry()
	return name, err
}

// Remove disconnects and removes a server by name.
func (m *Manager) Remove(name string) error {
	e, ok := m.servers[name]
	if !ok {
		return fmt.Errorf("no mcp server named %q", name)
	}
	if e.client != nil {
		_ = e.client.Close()
	}
	delete(m.servers, name)
	m.applyRegistry()
	return m.saveConfig()
}

// Reconnect drops and re-establishes the connection for an existing server.
func (m *Manager) Reconnect(ctx context.Context, name string) error {
	e, ok := m.servers[name]
	if !ok {
		return fmt.Errorf("no mcp server named %q", name)
	}
	client, err := Connect(ctx, e.cfg)
	if err != nil {
		e.client = nil
		e.tools = nil
		m.servers[name] = e
		m.applyRegistry()
		return fmt.Errorf("could not reconnect %q: %w", name, err)
	}
	specs, lerr := client.ListTools(ctx)
	e.client = client
	if lerr == nil {
		e.tools = specs
	}
	m.servers[name] = e
	m.applyRegistry()
	return lerr
}

// CloseAll disconnects every connected server. Call this on exit. It returns
// the first non-nil error.
func (m *Manager) CloseAll() error {
	var firstErr error
	for _, e := range m.servers {
		if e.client == nil {
			continue
		}
		if err := e.client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Command handles a /mcp slash command. It manages MCP servers at runtime
// (list/add/remove/reconnect) and persists them to the project config.
// fields is the full command split on whitespace, e.g. ["/mcp", "add", "{...}"].
func (m *Manager) Command(fields []string) string {
	if m == nil {
		return "no mcp servers are configured"
	}
	if len(fields) == 0 {
		return m.ListDetails()
	}
	// Find the subcommand token (first field that is not the "/mcp" prefix
	// or empty), and the remaining args after it.
	var sub string
	var args []string
	found := false
	for i, f := range fields {
		l := strings.ToLower(strings.TrimSpace(f))
		if strings.HasPrefix(l, "/mcp") || l == "" {
			continue
		}
		sub = l
		args = fields[i+1:]
		found = true
		break
	}
	if !found {
		return m.ListDetails()
	}
	switch sub {
	case "list":
		return m.ListDetails()
	case "add":
		if len(args) <= 0 {
			return "usage: /mcp add <json-config>\n  e.g. /mcp add {\"type\":\"stdio\",\"command\":\"npx\",\"args\":[\"-y\",\"@modelcontextprotocol/cli\"]}"
		}
		raw := strings.Join(args, " ")
		name, err := m.Add(context.Background(), raw)
		if err != nil {
			if name == "" {
				return err.Error()
			}
			return name + ": " + err.Error()
		}
		return fmt.Sprintf("added MCP server %q", name)
	case "remove", "rm", "delete":
		if len(args) <= 0 {
			return "usage: /mcp remove <name>"
		}
		name := strings.TrimSpace(args[0])
		if err := m.Remove(name); err != nil {
			return err.Error()
		}
		return fmt.Sprintf("removed MCP server %q", name)
	case "reconnect", "restart":
		if len(args) <= 0 {
			return "usage: /mcp reconnect <name>"
		}
		name := strings.TrimSpace(args[0])
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := m.Reconnect(ctx, name); err != nil {
			return err.Error()
		}
		return fmt.Sprintf("reconnected MCP server %q", name)
	default:
		return fmt.Sprintf("unknown mcp subcommand %q (try list/add/remove/reconnect)", sub)
	}
}

// ListToolsFor returns the tool specs for one server (empty if not connected).
func (m *Manager) ListToolsFor(name string) []ToolSpec {
	e, ok := m.servers[name]
	if !ok {
		return nil
	}
	return e.tools
}

// Namespaced returns the metis function name for a tool from a given server.
func (m *Manager) Namespaced(serverName, toolName string) string {
	return m.serverNamespaced(serverName, toolName)
}

// applyRegistry rebuilds the registry's MCP tools from current manager state.
// For each server it removes the server's name-prefixed tools and re-adds the
// current set (empty when not connected), leaving the built-in toolset alone.
func (m *Manager) applyRegistry() {
	for name, e := range m.servers {
		var newTools []tools.Tool
		if e.client != nil {
			newTools = make([]tools.Tool, 0, len(e.tools))
			for _, spec := range e.tools {
				fnName := m.serverNamespaced(name, spec.Name)
				newTools = append(newTools, tools.Tool{
					Name:        fnName,
					Description: spec.Description + " (MCP server: " + name + ")",
					Parameters:  spec.Parameters,
					Handler: func(ctx context.Context, args map[string]any) (string, error) {
						return e.client.Call(ctx, spec.Name, args)
					},
				})
			}
		}
		m.registry.ReloadMCP(name, newTools)
	}
}

// serverNamespaced mirrors Client.Namespaced without the Client instance.
func (m *Manager) serverNamespaced(serverName, toolName string) string {
	srv := sanitize(DefaultServerName(ServerConfig{Name: serverName}), 32)
	tl := sanitize(toolName, 32)
	full := srv + "__" + tl
	const maxLen = 64
	if len(full) > maxLen {
		full = full[:maxLen]
	}
	return full
}

// metaConfig is a local struct matching the subset of the project config file
// this manager manages. It exists so the mcp package does not import config
// (which would create a cycle: config already imports mcp for ServerConfig).
// Only mcp_servers is read/written; all other keys are preserved by writing
// back the raw object.
func (m *Manager) readServers() []ServerConfig {
	if m.configPath == "" {
		return nil
	}
	b, err := os.ReadFile(m.configPath)
	if err != nil {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil
	}
	var out []ServerConfig
	if v, ok := obj["mcp_servers"].([]any); ok {
		for _, item := range v {
			var s ServerConfig
			if err := json.Unmarshal(dataJSON(item), &s); err != nil {
				continue
			}
			out = append(out, s)
		}
		return out
	}
	return nil
}

func dataJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// saveConfig persists the current server set into the project config file,
// preserving any other existing keys. The file is a JSON object; we replace its
// mcp_servers array and write it back atomically.
func (m *Manager) saveConfig() error {
	if m.configPath == "" {
		return fmt.Errorf("no project config path configured")
	}
	obj := make(map[string]any)
	if b, err := os.ReadFile(m.configPath); err == nil {
		if err := json.Unmarshal(b, &obj); err != nil {
			return fmt.Errorf("parse %s: %w", m.configPath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", m.configPath, err)
	}
	servers := make([]ServerConfig, 0, len(m.servers))
	for _, e := range m.sorted() {
		servers = append(servers, e.cfg)
	}
	obj["mcp_servers"] = servers
	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(m.configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mcp-config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, m.configPath)
}

// sorted returns a stable slice of server entries in name order.
func (m *Manager) sorted() []serverEntry {
	out := make([]serverEntry, 0, len(m.servers))
	for _, e := range m.servers {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].name < out[j].name
	})
	return out
}

// status returns a short connection status string for a server entry.
func (e serverEntry) status() string {
	if e.client == nil {
		return "not connected"
	}
	return "connected"
}
