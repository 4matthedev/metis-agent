package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"metis-agent/internal/llm"
)

func newTestSession(t *testing.T, store *Store, workDir, id string) *Session {
	t.Helper()
	sess, err := store.NewNamed(id, workDir, "provider", "model")
	if err != nil {
		t.Fatal(err)
	}
	sess.Messages = []llm.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	return sess
}

func TestSaveLoadRoundTrip(t *testing.T) {
	store := NewStore(t.TempDir())
	work := t.TempDir()
	sess := newTestSession(t, store, work, "first")

	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	if got := store.Path(sess); got == "" {
		t.Fatal("Path() is empty after Save")
	}

	loaded, err := store.Load(work, "first")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != "first" || loaded.WorkDir != work {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.Provider != "provider" || loaded.Model != "model" {
		t.Fatalf("metadata lost: %+v", loaded)
	}
	if len(loaded.Messages) != 2 || loaded.Messages[0].Content != "hello" {
		t.Fatalf("messages lost: %+v", loaded.Messages)
	}
	if loaded.Version != fileVersion {
		t.Fatalf("version = %d, want %d", loaded.Version, fileVersion)
	}
}

func TestInvalidIdentifiersAreRejected(t *testing.T) {
	store := NewStore(t.TempDir())
	work := t.TempDir()

	for _, name := range []string{"", "../escape", "a/b", "with space", "-leading", strings.Repeat("x", 65)} {
		if _, err := store.NewNamed(name, work, "", ""); err == nil {
			t.Errorf("NewNamed(%q) accepted an invalid name", name)
		}
		if _, err := store.Load(work, name); err == nil {
			t.Errorf("Load(%q) accepted an invalid id", name)
		}
	}
}

func TestSaveRejectsEmptyWorkDir(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := &Session{ID: "x"}
	if err := store.Save(sess); err == nil {
		t.Fatal("Save accepted an empty WorkDir")
	}
}

func TestProjectsAreIsolated(t *testing.T) {
	store := NewStore(t.TempDir())
	a, b := t.TempDir(), t.TempDir()

	if err := store.Save(newTestSession(t, store, a, "only-a")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(b, "only-a"); err == nil {
		t.Fatal("session from project A leaked into project B")
	}
	infos, err := store.List(b, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Fatalf("project B list = %d sessions, want 0", len(infos))
	}
}

func TestListNewestFirstAndLatest(t *testing.T) {
	store := NewStore(t.TempDir())
	work := t.TempDir()

	older := newTestSession(t, store, work, "older")
	if err := store.Save(older); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	newer := newTestSession(t, store, work, "newer")
	newer.Messages = []llm.Message{{Role: "user", Content: "newest question"}}
	if err := store.Save(newer); err != nil {
		t.Fatal(err)
	}

	infos, err := store.List(work, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("list = %d, want 2", len(infos))
	}
	if infos[0].ID != "newer" {
		t.Fatalf("newest first: got %q", infos[0].ID)
	}
	if infos[0].Preview != "newest question" && !strings.Contains(infos[0].Preview, "newest") {
		t.Fatalf("preview = %q", infos[0].Preview)
	}

	latest, err := store.Latest(work)
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != "newer" {
		t.Fatalf("Latest() = %q, want newer", latest.ID)
	}

	// A limit is honored.
	if got, _ := store.List(work, 1); len(got) != 1 {
		t.Fatalf("List(limit=1) = %d", len(got))
	}
	if _, err := store.Latest(t.TempDir()); !os.IsNotExist(err) {
		t.Fatalf("Latest() on empty project = %v, want os.ErrNotExist", err)
	}
}

// TestSaveOverwritesAtomically checks that repeated saves replace the file and
// leave no temporary files behind.
func TestSaveOverwritesAtomically(t *testing.T) {
	store := NewStore(t.TempDir())
	work := t.TempDir()
	sess := newTestSession(t, store, work, "s")
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	sess.Messages = []llm.Message{{Role: "user", Content: "updated"}}
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(work, "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0].Content != "updated" {
		t.Fatalf("overwrite failed: %+v", loaded.Messages)
	}

	entries, err := os.ReadDir(store.projectDir(work))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file %q", e.Name())
		}
	}
}

func TestSavedFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions only")
	}
	store := NewStore(t.TempDir())
	work := t.TempDir()
	sess := newTestSession(t, store, work, "perm")
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.Path(sess))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("session mode = %o, want 600", perm)
	}
}

// TestListSkipsCorruptSessions ensures a damaged file cannot break the listing.
func TestListSkipsCorruptSessions(t *testing.T) {
	store := NewStore(t.TempDir())
	work := t.TempDir()
	if err := store.Save(newTestSession(t, store, work, "good")); err != nil {
		t.Fatal(err)
	}
	dir := store.projectDir(work)
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	infos, err := store.List(work, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].ID != "good" {
		t.Fatalf("list = %+v, want only the valid session", infos)
	}
}
