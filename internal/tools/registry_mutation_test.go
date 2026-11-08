package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMutationPathsSkipEmptyArguments(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistryWithOptions(Options{WorkDir: root})
	for _, name := range []string{"file_write", "file_delete", "file_move", "replace_in_files"} {
		if paths := reg.mutationPaths(name, map[string]any{}); len(paths) != 0 {
			t.Fatalf("%s with no arguments resolved to %#v", name, paths)
		}
	}
}

func TestMutationHookSnapshotsBeforeWrite(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistryWithOptions(Options{WorkDir: root, AutoApprove: true})
	var hooked []string
	reg.AddMutationHook(func(name string, _ map[string]any, paths []string) {
		if name != "file_write" {
			t.Fatalf("hook name = %q", name)
		}
		hooked = append(hooked, paths...)
	})
	input := []byte(`{"path":"note.txt","content":"hello"}`)
	if out := reg.Execute(context.Background(), "file_write", input); strings.HasPrefix(out, "error:") {
		t.Fatalf("file_write failed: %s", out)
	}
	if len(hooked) != 1 || filepath.Base(hooked[0]) != "note.txt" {
		t.Fatalf("hook paths = %#v", hooked)
	}
	if reg.Mutations() != 1 {
		t.Fatalf("mutations = %d, want 1", reg.Mutations())
	}
	reg.ResetMutations()
	if reg.Mutations() != 0 {
		t.Fatalf("mutations after reset = %d", reg.Mutations())
	}
}

func TestReadOnlyCallDoesNotCountAsMutation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistryWithOptions(Options{WorkDir: root})
	if out := reg.Execute(context.Background(), "file_read", []byte(`{"path":"a.txt"}`)); strings.HasPrefix(out, "error:") {
		t.Fatalf("file_read failed: %s", out)
	}
	if reg.Mutations() != 0 {
		t.Fatalf("read counted as mutation: %d", reg.Mutations())
	}
}

func TestRegistryRedactsToolOutput(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistryWithOptions(Options{WorkDir: root})
	reg.Add(&Tool{
		Name:        "leak",
		Description: "test-only",
		Parameters:  objectSchema(map[string]any{}),
		Handler: func(context.Context, map[string]any) (string, error) {
			return `token=ghp_abcdefghijklmnopqrstuvwxyz0123456789`, nil
		},
	})
	reg.SetRedactSecrets(true)
	out := reg.Execute(context.Background(), "leak", nil)
	if strings.Contains(out, "ghp_abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("secret survived: %s", out)
	}
	reg.SetRedactSecrets(false)
	out = reg.Execute(context.Background(), "leak", nil)
	if !strings.Contains(out, "ghp_abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("redaction stayed on: %s", out)
	}
}

func TestPreviewMutationBuildsDiff(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistryWithOptions(Options{WorkDir: root})
	diff := reg.previewMutation("file_edit", map[string]any{
		"path":     "a.txt",
		"old_text": "hello",
		"new_text": "world",
	})
	if !strings.Contains(diff, "-hello") || !strings.Contains(diff, "+world") {
		t.Fatalf("unexpected diff:\n%s", diff)
	}
	if got := reg.previewMutation("file_write", map[string]any{"path": "a.txt"}); got != "" {
		t.Fatalf("write without content should have no preview: %q", got)
	}
}
