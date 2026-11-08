package config

import (
	"testing"

	"metis-agent/internal/mcp"
)

func TestMergeServers(t *testing.T) {
	// Empty env returns base unchanged (same length, same elements).
	base := []mcp.ServerConfig{{Name: "a"}}
	if out := mergeServers(base, nil); len(out) != len(base) || out[0].Name != base[0].Name {
		t.Fatalf("empty env: len=%d name=%q, want len=%d name=%q", len(out), out[0].Name, len(base), base[0].Name)
	}

	// Env overrides an existing name (env wins).
	base = []mcp.ServerConfig{{Name: "a", URL: "http://file-a"}}
	env := []mcp.ServerConfig{{Name: "a", URL: "http://env-a"}}
	out := mergeServers(base, env)
	if len(out) != 1 {
		t.Fatalf("override len = %d", len(out))
	}
	if out[0].URL != "http://env-a" {
		t.Fatalf("override URL = %q, want env-a", out[0].URL)
	}

	// Env appends a new name.
	env2 := []mcp.ServerConfig{{Name: "b"}}
	out = mergeServers(base, env2)
	if len(out) != 2 {
		t.Fatalf("append len = %d", len(out))
	}

	// Matching by derived name: base entry with empty Name is matched via
	// DefaultServerName against an env entry with an explicit matching Name.
	base3 := []mcp.ServerConfig{{Command: "/usr/bin/nc"}} // derives to a sanitized name
	env3 := []mcp.ServerConfig{{Name: mcp.DefaultServerName(base3[0]), Type: "stdio", Command: "/usr/bin/nc2"}}
	out3 := mergeServers(base3, env3)
	if len(out3) != 1 {
		t.Fatalf("derived-name merge len = %d", len(out3))
	}
}
