package mcp

import (
	"context"
	"os"
	"strings"
	"testing"

	"metis-agent/internal/tools"
)

// TestManagerCommand exercises the /mcp slash-command handler against a fake
// registry, verifying list/add/remove/reconnect behavior and config persistence.
func TestManagerCommand(t *testing.T) {
	workDir := t.TempDir()
	cfgPath := workDir + "/.metis-agent.json"
	cfg := tools.NewRegistryWithOptions(tools.Options{WorkDir: workDir})
	m := NewManager(cfg, cfgPath)

	// Bare command -> list (no servers).
	if out := m.Command([]string{"/mcp"}); !hasPrefix(out, "no MCP servers configured") {
		t.Fatalf("bare /mcp expected no servers, got: %q", out)
	}
	if out := m.Command([]string{"list"}); !hasPrefix(out, "no MCP servers configured") {
		t.Fatalf("list expected no servers, got: %q", out)
	}

	// Unknown subcommand.
	if !hasPrefix(m.Command([]string{"/mcp", "bogus"}), "unknown mcp subcommand") {
		t.Fatal("expected unknown mcp subcommand message")
	}

	// add without json -> usage.
	if !hasPrefix(m.Command([]string{"/mcp", "add"}), "usage: /mcp add") {
		t.Fatalf("expected add usage")
	}

	// remove without name -> usage.
	if !hasPrefix(m.Command([]string{"/mcp", "remove"}), "usage: /mcp remove") {
		t.Fatal("expected remove usage")
	}

	// reconnect without name -> usage.
	if !hasPrefix(m.Command([]string{"/mcp", "reconnect"}), "usage: /mcp reconnect") {
		t.Fatal("expected reconnect usage")
	}

	// A fake add that fails to connect: the server is still persisted
	// (listed) but marked not connected. Use an unreachable stdio command.
	raw := `{"type":"stdio","name":"fake","command":"/bin/definitely-not-a-real-mcp-server"}`
	out := m.Command([]string{"/mcp", "add", raw})
	if !hasPrefix(out, "fake:") {
		t.Fatalf("expected add failure message, got: %q", out)
	}

	// Now list should show the server (not connected).
	out = m.Command([]string{"/mcp", "list"})
	if !contains(out, "fake") || !contains(out, "not connected") {
		t.Fatalf("expected fake with not-connected status, got: %q", out)
	}

	// Config file should have been written with the fake server.
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("expected config file to exist: %v", err)
	}
	if !contains(string(b), "fake") {
		t.Fatalf("config file should contain fake server")
	}

	// remove should succeed and drop it.
	if !hasPrefix(m.Command([]string{"/mcp", "remove", "fake"}), "removed") {
		t.Fatal("expected removed message")
	}
	if out := m.Command([]string{"/mcp", "list"}); !hasPrefix(out, "no MCP servers configured") {
		t.Fatalf("expected empty after remove, got: %q", out)
	}
}

// TestManagerReloadRoundTrip verifies that servers persisted to config are
// loaded and re-registered on a fresh Load (simulating a restart).
func TestManagerReloadRoundTrip(t *testing.T) {
	workDir := t.TempDir()
	cfgPath := workDir + "/.metis-agent.json"
	cfg := tools.NewRegistryWithOptions(tools.Options{WorkDir: workDir})
	m := NewManager(cfg, cfgPath)

	// Add a server (will not connect, but is persisted).
	raw := `{"type":"stdio","name":"rt","command":"/bin/nope"}`
	out := m.Command([]string{"/mcp", "add", raw})
	if !hasPrefix(out, "rt:") {
		t.Fatalf("expected add failure message for rt, got: %q", out)
	}

	// Fresh manager over the same config should load it on Load.
	m2 := NewManager(cfg, cfgPath)
	m2.Load(context.Background())
	if out := m2.Command([]string{"/mcp", "list"}); !contains(out, "rt") {
		t.Fatalf("fresh manager should list rt after Load, got: %q", out)
	}
}

// TestManagerLayerServers verifies that .conf-parsed servers layer on top of
// the legacy JSON servers, with the .conf side winning on name conflicts.
func TestManagerLayerServers(t *testing.T) {
	workDir := t.TempDir()
	cfgPath := workDir + "/.metis-agent.json"
	cfg := tools.NewRegistryWithOptions(tools.Options{WorkDir: workDir})

	// Seed the legacy JSON config with two servers, then load.
	if err := os.WriteFile(cfgPath, []byte(`{
  "mcp_servers": [
    {"type": "stdio", "name": "legacy", "command": "/bin/legacy"},
    {"type": "stdio", "name": "other", "command": "/bin/other"}
  ]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(cfg, cfgPath)
	m.Load(context.Background())
	if len(m.servers) != 2 {
		t.Fatalf("expected 2 JSON servers, got %d", len(m.servers))
	}
	if m.servers["legacy"].cfg.Command != "/bin/legacy" {
		t.Fatalf("legacy command = %q", m.servers["legacy"].cfg.Command)
	}

	// Layer .conf servers: "legacy" conflicts (conf must win), "new" is added.
	confServers := []ServerConfig{
		{Name: "legacy", Type: "stdio", Command: "/bin/conf"},
		{Type: "stdio", Name: "new", Command: "/bin/new"},
	}
	m.LayerServers(context.Background(), confServers)
	if m.servers["legacy"].cfg.Command != "/bin/conf" {
		t.Fatalf("conf did not win conflict: legacy command = %q", m.servers["legacy"].cfg.Command)
	}
	if _, ok := m.servers["other"]; !ok {
		t.Fatal("expected 'other' from JSON to survive layering")
	}
	if m.servers["new"].cfg.Command != "/bin/new" {
		t.Fatalf("new server not layered: command = %q", m.servers["new"].cfg.Command)
	}
	if len(m.servers) != 3 {
		t.Fatalf("expected 3 servers after layering, got %d", len(m.servers))
	}
	if out := m.Command([]string{"list"}); !strings.Contains(out, "legacy") ||
		!strings.Contains(out, "other") || !strings.Contains(out, "new") {
		t.Fatalf("list should show layered servers: %q", out)
	}

	// Empty layering is a no-op.
	m.LayerServers(context.Background(), nil)
	if len(m.servers) != 3 {
		t.Fatalf("empty LayerServers changed count: %d", len(m.servers))
	}
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && strings.HasPrefix(s, p)
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
