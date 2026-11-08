package skills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	skDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", skDir, err)
	}
	if err := os.WriteFile(filepath.Join(skDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write %s/SKILL.md: %v", skDir, err)
	}
}

func TestParseSkillWithFrontmatter(t *testing.T) {
	work := t.TempDir()
	p := filepath.Join(work, "sk", "SKILL.md")
	content := `---
name: greet
description: Greet a person by name.
run: npx skills add greet
---
## Body
Say hi to {{name}}.
`
	os.MkdirAll(filepath.Join(work, "sk"), 0755)
	os.WriteFile(p, []byte(content), 0644)

	sk, err := ParseSkill(p)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	if sk.Name != "greet" {
		t.Fatalf("name = %q, want greet", sk.Name)
	}
	if sk.Description != "Greet a person by name." {
		t.Fatalf("description = %q", sk.Description)
	}
	if sk.Run != "npx skills add greet" {
		t.Fatalf("run = %q", sk.Run)
	}
	if !strings.HasPrefix(sk.Body, "## Body") {
		t.Fatalf("body = %q, want it to start with '## Body'", sk.Body)
	}
}

func TestParseSkillNoFrontmatter(t *testing.T) {
	work := t.TempDir()
	p := filepath.Join(work, "nfm", "SKILL.md")
	content := "Just a body, no frontmatter.\nSecond line.\n"
	os.MkdirAll(filepath.Join(work, "nfm"), 0755)
	os.WriteFile(p, []byte(content), 0644)

	sk, err := ParseSkill(p)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	if sk.Name != "nfm" {
		t.Fatalf("name should be dir name, got %q", sk.Name)
	}
	if !strings.Contains(sk.Body, "Just a body") {
		t.Fatalf("body = %q", sk.Body)
	}
}

func TestManagerDiscoverProjectAndGlobal(t *testing.T) {
	// Build a global-like dir for the test.
	g := t.TempDir()
	writeSkill(t, g, "gsk", `---
name: gsk
description: global test skill
run: echo hi
---
Global body.
`)
	writeSkill(t, g, "hsk", `---
name: hsk
description: another global skill
---
Global body two.
`)

	// Set the global dir env to the test dir, and point the project dir to a fresh one.
	oldGlobal := os.Getenv("METIS_GLOBAL_SKILLS_DIR")
	os.Setenv("METIS_GLOBAL_SKILLS_DIR", g)
	defer func() {
		if oldGlobal == "" {
			os.Unsetenv("METIS_GLOBAL_SKILLS_DIR")
		} else {
			os.Setenv("METIS_GLOBAL_SKILLS_DIR", oldGlobal)
		}
	}()

	dir := t.TempDir()
	m := NewManager(dir)

	sks := m.List(context.Background())
	names := make(map[string]bool)
	for _, sk := range sks {
		names[sk.Name] = true
	}
	if !names["gsk"] || !names["hsk"] {
		t.Fatalf("expected gsk+hsk, got: %v", sks)
	}

	// Add a project skill at runtime and invalidate.
	writeSkill(t, dir+"/.metis-agent/skills", "psk", `---
name: psk
description: project test skill
---
Project body.
`)
	m.Invalidate()
	sks = m.List(context.Background())
	found := false
	for _, sk := range sks {
		if sk.Name == "psk" && sk.Kind == "project" {
			found = true
		}
	}
	if !found {
		t.Fatalf("project skill psk not found after Invalidate; got %v", sks)
	}
}

func TestManagerCommand(t *testing.T) {
	// Point the global dir at an empty temp dir so the test is deterministic
	// and doesn't pick up real ~/.agents/skills.
	g := t.TempDir()
	os.Setenv("METIS_GLOBAL_SKILLS_DIR", g)
	defer os.Unsetenv("METIS_GLOBAL_SKILLS_DIR")

	work := t.TempDir()
	m := NewManager(work)

	// list (no skills)
	out := m.Command(context.Background(), []string{"skills"})
	if !strings.Contains(out, "no skills found") {
		t.Fatalf("list: %q", out)
	}

	// unknown subcommand
	out = m.Command(context.Background(), []string{"skills", "bogus"})
	if !strings.Contains(out, "unknown skills subcommand") {
		t.Fatalf("bogus: %q", out)
	}

	// info with no arg
	if !strings.HasPrefix(m.Command(context.Background(), []string{"skills", "info"}), "usage: /skills info") {
		t.Fatal("expected info usage")
	}
}

func TestManagerRunCommand(t *testing.T) {
	work := t.TempDir()
	m := NewManager(work)
	// Create a project skill with a run command.
	writeSkill(t, work+"/.metis-agent/skills", "runner", `---
name: runner
description: runs a command
run: echo hello
---
body
`)
	m.Invalidate()

	cmd, ok := m.RunCommand(context.Background(), []string{"runner"})
	if !ok || cmd != "echo hello" {
		t.Fatalf("run command ok=%v, got %q, want echo hello", ok, cmd)
	}
	// unknown skill
	cmd, ok2 := m.RunCommand(context.Background(), []string{"nope"})
	if ok2 || !strings.Contains(cmd, "no skill") {
		t.Fatalf("unknown: %q", cmd)
	}
}
