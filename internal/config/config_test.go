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

func TestApplyEnvOverridesAndParses(t *testing.T) {
	v := Defaults()
	t.Setenv("METIS_PROVIDER", "ollama")
	t.Setenv("METIS_MODEL", "qwen")
	t.Setenv("METIS_BASE_URL", "http://127.0.0.1:11434")
	t.Setenv("METIS_TEMPERATURE", "0.7")
	t.Setenv("METIS_MAX_STEPS", "12")
	t.Setenv("METIS_WINDOW", "20")
	t.Setenv("METIS_SHELL", "false")
	t.Setenv("METIS_SHELL_TIMEOUT_SEC", "30")
	t.Setenv("METIS_HIGHLIGHT", "DARK")
	t.Setenv("METIS_AUTO_APPROVE", "true")

	if err := ApplyEnv(&v); err != nil {
		t.Fatal(err)
	}
	if v.Provider != "ollama" || v.Model != "qwen" || v.BaseURL != "http://127.0.0.1:11434" {
		t.Fatalf("provider/model/base_url not applied: %+v", v)
	}
	if v.Temperature != 0.7 || v.MaxSteps != 12 || v.Window != 20 {
		t.Fatalf("numeric env not applied: %+v", v)
	}
	if v.Shell || v.ShellTimeoutSec != 30 || v.Highlight != "dark" || !v.AutoApprove {
		t.Fatalf("bool/string env not applied: %+v", v)
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("valid env produced invalid values: %v", err)
	}
}

func TestApplyEnvRejectsBadNumbers(t *testing.T) {
	cases := map[string]string{
		"METIS_TEMPERATURE": "hot",
		"METIS_MAX_STEPS":   "many",
		"METIS_SHELL":       "maybe",
	}
	for key, val := range cases {
		v := Defaults()
		t.Setenv(key, val)
		if err := ApplyEnv(&v); err == nil {
			t.Errorf("ApplyEnv accepted %s=%q", key, val)
		}
	}
}

func TestApplyOverlaysOnlySetFields(t *testing.T) {
	base := Defaults()
	provider := "openai"
	temp := 1.1
	f := File{Provider: &provider, Temperature: &temp}
	f.Apply(&base)

	if base.Provider != "openai" || base.Temperature != 1.1 {
		t.Fatalf("set fields not applied: %+v", base)
	}
	// Unset fields keep their defaults.
	if base.MaxSteps != 40 || base.Window != 64 || base.ShellTimeoutSec != 120 {
		t.Fatalf("unset fields were clobbered: %+v", base)
	}
}

func TestValidateRejectsOutOfRange(t *testing.T) {
	bad := []Values{
		func() Values { v := Defaults(); v.Provider = " "; return v }(),
		func() Values { v := Defaults(); v.Temperature = 3; return v }(),
		func() Values { v := Defaults(); v.MaxSteps = 0; return v }(),
		func() Values { v := Defaults(); v.Window = 0; return v }(),
		func() Values { v := Defaults(); v.ShellTimeoutSec = 0; return v }(),
		func() Values { v := Defaults(); v.Highlight = "neon"; return v }(),
	}
	for i, v := range bad {
		if err := v.Validate(); err == nil {
			t.Errorf("case %d: Validate accepted invalid values %+v", i, v)
		}
	}
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("defaults should validate: %v", err)
	}
}

func TestProjectPath(t *testing.T) {
	if got := ProjectPath("/tmp/proj"); got != "/tmp/proj/.metis-agent.json" {
		t.Fatalf("ProjectPath = %q", got)
	}
	if got := ProjectPath(""); got != ".metis-agent.json" {
		t.Fatalf("ProjectPath(\"\") = %q", got)
	}
}
