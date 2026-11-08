package agent

import (
	"context"
	"strings"
	"testing"

	"metis-agent/internal/tools"
)

// TestPlannerPolicyIsEnforced proves that Planner mode is a hard gate: the
// mutating tools are hidden from the model and refused even when auto-approve
// is on.
func TestPlannerPolicyIsEnforced(t *testing.T) {
	reg := tools.NewRegistry(t.TempDir()) // auto-approve is ON in this constructor
	a := New(Config{LLM: &fakeCompleter{}, Registry: reg, System: "sys"})

	a.SetMode(ModePlanner)

	visible := map[string]bool{}
	for _, spec := range reg.Specs() {
		visible[spec.Function.Name] = true
	}
	for _, banned := range []string{
		"shell", "file_write", "file_edit", "multi_edit", "replace_in_files",
		"make_dir", "file_move", "file_copy", "file_delete", "download_file",
	} {
		if visible[banned] {
			t.Errorf("%s must not be offered in Planner mode", banned)
		}
	}
	for _, allowed := range []string{"file_read", "search", "list_dir", "git_diff", "todo_write"} {
		if !visible[allowed] {
			t.Errorf("%s should be available in Planner mode", allowed)
		}
	}

	out := reg.Execute(context.Background(), "file_write", []byte(`{"path":"x.txt","content":"y"}`))
	if !strings.Contains(out, "not available") {
		t.Fatalf("file_write in Planner mode = %q, want a policy refusal", out)
	}
	if out := reg.Execute(context.Background(), "shell", []byte(`{"command":"touch x"}`)); !strings.Contains(out, "not available") {
		t.Fatalf("shell in Planner mode = %q, want a policy refusal", out)
	}
}

func TestPlannerPolicyRules(t *testing.T) {
	p := policyForMode(ModePlanner)
	if p == nil {
		t.Fatal("Planner mode returned a nil policy")
	}
	cases := []struct {
		name  string
		args  map[string]any
		allow bool
	}{
		{"skills", map[string]any{"action": "list"}, true},
		{"skills", map[string]any{"action": "info"}, true},
		{"skills", map[string]any{"action": "run"}, false},
		{"http_fetch", map[string]any{"url": "https://x"}, true},
		{"http_fetch", map[string]any{"method": "get"}, true},
		{"http_fetch", map[string]any{"method": "POST"}, false},
		{"srv__danger", map[string]any{}, false},
		{"file_write", map[string]any{}, false},
		{"unknown_future_tool", map[string]any{}, false},
	}
	for _, c := range cases {
		ok, reason := p(c.name, c.args)
		if ok != c.allow {
			t.Errorf("policy(%q, %v) = %v (%q), want %v", c.name, c.args, ok, reason, c.allow)
		}
		if !ok && strings.TrimSpace(reason) == "" {
			t.Errorf("policy(%q, %v) refused without a reason", c.name, c.args)
		}
	}

	if policyForMode(ModeGeneral) != nil {
		t.Error("General mode should not restrict the toolset")
	}
}

func TestModeSwitchRestoresTools(t *testing.T) {
	reg := tools.NewRegistry(t.TempDir())
	a := New(Config{LLM: &fakeCompleter{}, Registry: reg, System: "sys"})

	a.SetMode(ModePlanner)
	if out := reg.Execute(context.Background(), "shell", []byte(`{"command":"echo hi"}`)); !strings.Contains(out, "not available") {
		t.Fatalf("Planner should block shell, got %q", out)
	}

	a.SetMode(ModeBuilder)
	out := reg.Execute(context.Background(), "shell", []byte(`{"command":"echo hi"}`))
	if strings.Contains(out, "not available") {
		t.Fatalf("Builder should allow shell again, got %q", out)
	}
}

func TestExplicitMCPToolBlockedInPlanner(t *testing.T) {
	reg := tools.NewRegistry(t.TempDir())
	called := false
	reg.RegisterExternalTool(&tools.Tool{
		Name:        "srv__danger",
		Description: "does something",
		Parameters:  map[string]any{"type": "object"},
		Handler: func(context.Context, map[string]any) (string, error) {
			called = true
			return "ran", nil
		},
	}, "srv")

	a := New(Config{LLM: &fakeCompleter{}, Registry: reg, System: "sys"})
	a.SetMode(ModePlanner)

	out := reg.Execute(context.Background(), "srv__danger", []byte(`{}`))
	if !strings.Contains(out, "not available") {
		t.Fatalf("MCP tool in Planner mode = %q, want refusal", out)
	}
	if called {
		t.Fatal("blocked MCP tool handler ran")
	}
}

// TestPlannerPolicyAppliedAtConstruction covers the Config.Mode path (used by
// non-TUI frontends) rather than SetMode.
func TestPlannerPolicyAppliedAtConstruction(t *testing.T) {
	reg := tools.NewRegistry(t.TempDir())
	New(Config{LLM: &fakeCompleter{}, Registry: reg, System: "sys", Mode: ModePlanner})

	if out := reg.Execute(context.Background(), "file_write", []byte(`{"path":"x","content":"y"}`)); !strings.Contains(out, "not available") {
		t.Fatalf("Config.Mode did not install the Planner policy: %q", out)
	}
}
