package instructions

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadReadsProjectAndExtraFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", t.TempDir())
	} else {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("Use tabs.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".metis-agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".metis-agent", "rules.md"), []byte("Never force-push.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extra.md"), []byte("Write tests.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	docs := Load(dir, []string{"extra.md"})
	if len(docs) != 3 {
		t.Fatalf("got %d docs: %#v", len(docs), docs)
	}
	prompt := Prompt(docs)
	for _, want := range []string{"Use tabs.", "Never force-push.", "Write tests."} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestLoadIgnoresMissingAndEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", t.TempDir())
	} else {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if docs := Load(dir, []string{"does-not-exist.md"}); len(docs) != 0 {
		t.Fatalf("got %d docs, want 0", len(docs))
	}
}
