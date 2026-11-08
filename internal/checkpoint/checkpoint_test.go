package checkpoint

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func newManager(t *testing.T) (*Manager, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", t.TempDir())
	} else {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
	}
	dir := t.TempDir()
	m, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m, dir
}

func TestUndoRestoresFileAndRemovesNewFile(t *testing.T) {
	m, dir := newManager(t)
	existing := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(existing, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	created := filepath.Join(dir, "b.txt")

	if err := m.Begin("test"); err != nil {
		t.Fatal(err)
	}
	m.Snapshot(existing)
	m.Snapshot(created)
	if err := os.WriteFile(existing, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.End(); err != nil {
		t.Fatal(err)
	}

	res, err := m.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if res.Restored != 1 {
		t.Fatalf("restored = %d, want 1", res.Restored)
	}
	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "before" {
		t.Fatalf("existing file = %q, want before", got)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("created file still exists: %v", err)
	}
	if _, err := m.Undo(); err == nil {
		t.Fatal("second undo should fail when no checkpoint remains")
	}
}

func TestUndoRestoresDeletedDirectory(t *testing.T) {
	m, dir := newManager(t)
	sub := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "x.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.Begin("delete"); err != nil {
		t.Fatal(err)
	}
	m.Snapshot(sub)
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}
	if err := m.End(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Undo(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(sub, "x.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "package pkg\n" {
		t.Fatalf("restored content = %q", got)
	}
}

func TestEmptyTurnIsDiscarded(t *testing.T) {
	m, _ := newManager(t)
	if err := m.Begin("noop"); err != nil {
		t.Fatal(err)
	}
	if err := m.End(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Undo(); err == nil {
		t.Fatal("empty turn should not create a checkpoint")
	}
}
