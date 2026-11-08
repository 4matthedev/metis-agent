package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runTool(t *testing.T, reg *Registry, name, args string) string {
	t.Helper()
	return reg.Execute(context.Background(), name, []byte(args))
}

func TestSafePathRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	cases := []string{"", "/etc/passwd", "../outside", "a/../../b"}
	for _, c := range cases {
		if _, err := safePath(root, c); err == nil {
			t.Errorf("safePath(%q) = nil error, want rejection", c)
		}
	}
	if _, err := safePath(root, "sub/file.txt"); err != nil {
		t.Errorf("safePath(sub/file.txt): %v", err)
	}
}

func TestSafePathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	if _, err := safePath(root, "link/secret"); err == nil {
		t.Fatal("symlink escape was allowed")
	}
}

func TestFileWriteThenRead(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(root)
	out := runTool(t, reg, "file_write", `{"path":"sub/note.txt","content":"hello metis"}`)
	if strings.HasPrefix(out, "error") {
		t.Fatalf("file_write: %s", out)
	}
	got := runTool(t, reg, "file_read", `{"path":"sub/note.txt"}`)
	if got != "hello metis" {
		t.Fatalf("file_read = %q", got)
	}
	b, err := os.ReadFile(filepath.Join(root, "sub", "note.txt"))
	if err != nil || string(b) != "hello metis" {
		t.Fatalf("on-disk content = %q, err %v", b, err)
	}
}

func TestUnknownTool(t *testing.T) {
	reg := NewRegistry(t.TempDir())
	out := runTool(t, reg, "nope", `{}`)
	if !strings.Contains(out, "unknown tool") {
		t.Fatalf("out = %q", out)
	}
}

func TestFileReadLineRange(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(root)
	if out := runTool(t, reg, "file_write", `{"path":"f.txt","content":"one\ntwo\nthree\nfour"}`); strings.HasPrefix(out, "error") {
		t.Fatalf("file_write: %s", out)
	}
	got := runTool(t, reg, "file_read", `{"path":"f.txt","start_line":2,"end_line":3}`)
	if !strings.HasPrefix(got, "two\nthree") {
		t.Fatalf("range read = %q", got)
	}
}

func TestFileEdit(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(root)
	runTool(t, reg, "file_write", `{"path":"code.go","content":"package main\n\nfunc add(a, b int) int {\n\treturn a - b\n}\n"}`)

	out := runTool(t, reg, "file_edit", `{"path":"code.go","old_text":"return a - b","new_text":"return a + b"}`)
	if strings.HasPrefix(out, "error") {
		t.Fatalf("file_edit: %s", out)
	}
	b, _ := os.ReadFile(filepath.Join(root, "code.go"))
	if !strings.Contains(string(b), "return a + b") {
		t.Fatalf("edit not applied: %q", b)
	}

	// Missing text is a clear error.
	if out := runTool(t, reg, "file_edit", `{"path":"code.go","old_text":"does not exist","new_text":"x"}`); !strings.Contains(out, "not found") {
		t.Fatalf("missing old_text = %q", out)
	}

	// Duplicate text requires replace_all.
	runTool(t, reg, "file_write", `{"path":"dup.txt","content":"x\nx\n"}`)
	if out := runTool(t, reg, "file_edit", `{"path":"dup.txt","old_text":"x","new_text":"y"}`); !strings.Contains(out, "appears 2 times") {
		t.Fatalf("duplicate edit = %q", out)
	}
	if out := runTool(t, reg, "file_edit", `{"path":"dup.txt","old_text":"x","new_text":"y","replace_all":true}`); strings.HasPrefix(out, "error") {
		t.Fatalf("replace_all: %s", out)
	}
	b, _ = os.ReadFile(filepath.Join(root, "dup.txt"))
	if string(b) != "y\ny\n" {
		t.Fatalf("replace_all content = %q", b)
	}
}

func TestListDir(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(root)
	for _, p := range []string{"a.go", "src/b.go", ".hidden/c", "node_modules/x/y.js"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := runTool(t, reg, "list_dir", `{"depth":3}`)
	for _, want := range []string{"a.go", "src/", "src/b.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("list_dir missing %q in %q", want, out)
		}
	}
	for _, unwanted := range []string{".hidden", "node_modules"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("list_dir should skip %q: %q", unwanted, out)
		}
	}
	if out := runTool(t, reg, "list_dir", `{"depth":3,"include_hidden":true}`); !strings.Contains(out, ".hidden/") {
		t.Errorf("include_hidden did not list dotdirs: %q", out)
	}
}

func TestSearch(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(root)
	runTool(t, reg, "file_write", `{"path":"src/a.py","content":"def add(a, b):\n    return a + b\n"}`)
	runTool(t, reg, "file_write", `{"path":"src/b.py","content":"def sub(a, b):\n    return a - b\n"}`)
	runTool(t, reg, "file_write", `{"path":"notes.txt","content":"def add is documented here\n"}`)

	out := runTool(t, reg, "search", `{"pattern":"def add","include":"*.py"}`)
	if !strings.Contains(out, "src/a.py:1:") {
		t.Errorf("search missed a.py: %q", out)
	}
	if strings.Contains(out, "notes.txt") {
		t.Errorf("include filter failed: %q", out)
	}
	if out := runTool(t, reg, "search", `{"pattern":"nomatch-xyz"}`); !strings.Contains(out, "no matches") {
		t.Errorf("no-match result = %q", out)
	}
}

func TestShellSuccessAndFailure(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(root)

	out := runTool(t, reg, "shell", `{"command":"printf 'hello\\n'"}`)
	if !strings.Contains(out, "exit code: 0") || !strings.Contains(out, "hello") {
		t.Fatalf("shell output = %q", out)
	}

	out = runTool(t, reg, "shell", `{"command":"echo bad >&2; exit 3"}`)
	if !strings.Contains(out, "exit code: 3") || !strings.Contains(out, "bad") {
		t.Fatalf("failing shell output = %q", out)
	}
}

func TestShellWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(root)
	out := runTool(t, reg, "shell", `{"command":"pwd","cwd":"sub"}`)
	if !strings.Contains(out, filepath.Join(root, "sub")) {
		t.Fatalf("cwd not honoured: %q", out)
	}
}

func TestShellTimeoutKillsChildren(t *testing.T) {
	reg := NewRegistry(t.TempDir())
	start := time.Now()
	out := runTool(t, reg, "shell", `{"command":"sh -c 'sleep 10 & wait'","timeout_sec":1}`)
	if !strings.Contains(out, "timed out") {
		t.Fatalf("timeout output = %q", out)
	}
	if elapsed := time.Since(start); elapsed > 6*time.Second {
		t.Fatalf("timeout took too long (children not killed?): %s", elapsed)
	}
}

func TestShellCancellation(t *testing.T) {
	reg := NewRegistry(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	out := reg.Execute(ctx, "shell", []byte(`{"command":"sleep 10"}`))
	if !strings.Contains(out, "context canceled") {
		t.Fatalf("cancelled shell = %q", out)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cancellation took too long: %s", elapsed)
	}
}

func TestShellDisabled(t *testing.T) {
	reg := NewRegistryWithOptions(Options{WorkDir: t.TempDir()})
	out := runTool(t, reg, "shell", `{"command":"echo hi"}`)
	if !strings.Contains(out, "unknown tool") {
		t.Fatalf("shell should be disabled: %q", out)
	}
	for _, tl := range reg.List() {
		if tl.Name == "shell" {
			t.Fatal("shell tool registered despite EnableShell=false")
		}
	}
}

// TestExternalToolRegistration covers MCP-style external tools: they are
// registered, appear in the list, and always require approval.
func TestExternalToolRegistration(t *testing.T) {
	reg := NewRegistryWithOptions(Options{WorkDir: t.TempDir()})
	reg.RegisterExternalTool(&Tool{
		Name:        "myserver__mytool",
		Description: "an external MCP tool",
		Parameters:  map[string]any{"type": "object"},
		Handler:     func(ctx context.Context, args map[string]any) (string, error) { return "done", nil },
	}, "myserver")

	// It appears in the list.
	found := false
	for _, tl := range reg.List() {
		if tl.Name == "myserver__mytool" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("external tool not in registry list")
	}

	// Duplicate registration is ignored (byName check).
	reg.RegisterExternalTool(&Tool{Name: "myserver__mytool"}, "myserver")

	// It always requires approval, with an MCP reason.
	req, ok := reg.needApproval("myserver__mytool", map[string]any{})
	if !ok {
		t.Fatal("external tool should require approval")
	}
	if req.Tool != "myserver__mytool" {
		t.Fatalf("req.Tool = %q", req.Tool)
	}
	if !strings.Contains(req.Reason, "MCP") {
		t.Fatalf("reason = %q, want MCP mention", req.Reason)
	}
}

// TestExternalOverrideApproval shows the approval gate is consulted for an
// external tool (using the auto-approve registry it should not ask). Here we
// use a non-auto registry to confirm the approver path is reached; since no
// approver is installed and auto-approve is off, a dangerous call would block.
// We only assert the classification, not the full execution path.
func TestExternalClassification(t *testing.T) {
	reg := NewRegistryWithOptions(Options{WorkDir: t.TempDir(), AutoApprove: false})
	reg.RegisterExternalTool(&Tool{
		Name:        "sv__t",
		Description: "x",
		Handler:     func(context.Context, map[string]any) (string, error) { return "", nil },
	}, "sv")
	_, ok := reg.needApproval("sv__t", nil)
	if !ok {
		t.Fatal("expected external tool to need approval")
	}
}

// TestBuiltinsStillNeedApproval confirms built-in dangerous tools are still
// classified (regression for the early external check).
func TestBuiltinsStillNeedApproval(t *testing.T) {
	reg := NewRegistryWithOptions(Options{WorkDir: t.TempDir(), AutoApprove: false})
	req, ok := reg.needApproval("shell", map[string]any{"command": "true"})
	if !ok || req.Tool != "shell" {
		t.Fatalf("shell should need approval; got ok=%v tool=%q", ok, req.Tool)
	}
	// A read-only built-in should not need approval.
	_, ok = reg.needApproval("file_read", map[string]any{"path": "a.txt"})
	if ok {
		t.Fatal("file_read should not need approval")
	}
}
