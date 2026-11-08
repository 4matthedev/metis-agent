package main

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"metis-agent/internal/agent"
)

func TestSplitCSV(t *testing.T) {
	got := splitCSV(" a, b ,,c ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
	if out := splitCSV("   "); len(out) != 0 {
		t.Fatalf("blank input = %#v, want empty", out)
	}
}

func TestCleanCommitMessage(t *testing.T) {
	cases := map[string]string{
		"```\nfeat: add router\n```": "feat: add router",
		`"fix: handle empty input"`:  "fix: handle empty input",
		"  chore: tidy  \n":          "chore: tidy",
	}
	for in, want := range cases {
		if got := cleanCommitMessage(in); got != want {
			t.Fatalf("cleanCommitMessage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadOnlyPolicy(t *testing.T) {
	for _, name := range []string{"file_read", "search", "web_search", "git_diff", "repo_map"} {
		if ok, _ := readOnlyPolicy(name, nil); !ok {
			t.Fatalf("%s should be allowed for a read-only agent", name)
		}
	}
	for _, name := range []string{"file_write", "file_edit", "shell", "download_file", "replace_in_files", "subagent"} {
		ok, reason := readOnlyPolicy(name, nil)
		if ok {
			t.Fatalf("%s should be refused for a read-only agent", name)
		}
		if reason == "" {
			t.Fatalf("%s refusal should explain why", name)
		}
	}
}

func TestRunVerifyCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	dir := t.TempDir()
	out, err := runVerifyCommand(context.Background(), "printf ok", dir, 5*time.Second)
	if err != nil {
		t.Fatalf("verify: %v (%s)", err, out)
	}
	if out != "ok" {
		t.Fatalf("output = %q, want ok", out)
	}

	_, err = runVerifyCommand(context.Background(), "sh -c 'echo bad; exit 3'", dir, 5*time.Second)
	if err == nil {
		t.Fatal("expected a non-zero verification to fail")
	}
}

func TestRunVerifyCommandTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	_, err := runVerifyCommand(context.Background(), "sleep 5", t.TempDir(), 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
}

func TestJSONSinkWritesOneLinePerEvent(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	sink := jsonSink()
	defer func() { os.Stdout = old }()
	sink(agent.Event{Kind: agent.EventText, Step: 1, Text: "hello"})
	sink(agent.Event{Kind: agent.EventDone, Step: 2, Text: "bye"})
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old

	var lines []string
	dec := json.NewDecoder(r)
	for {
		var line map[string]any
		if err := dec.Decode(&line); err != nil {
			break
		}
		typ, _ := line["type"].(string)
		if typ == "" {
			t.Fatalf("event missing type: %#v", line)
		}
		lines = append(lines, typ)
	}
	if len(lines) != 2 || lines[0] != "text" || lines[1] != "done" {
		t.Fatalf("events = %#v", lines)
	}
}
