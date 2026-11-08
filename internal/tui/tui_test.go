package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"metis-agent/internal/agent"
	"metis-agent/internal/llm"
)

func testModel() *Model {
	a := agent.New(agent.Config{})
	return newModel(a, Options{
		Model:  "Qwen3.8-27B",
		Server: "http://127.0.0.1:9931",
		Dir:    ".",
		Tools:  []string{"time", "file_read", "file_write"},
	})
}

func TestViewRendersChrome(t *testing.T) {
	m := testModel()
	m.input.Focus()
	m.layout()
	view := m.View()
	for _, want := range []string{"metis", "Qwen3.8-27B", "ready", "workspace"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n%s", want, view)
		}
	}
}

func TestResizeDoesNotPanic(t *testing.T) {
	m := testModel()
	for _, size := range []tea.WindowSizeMsg{
		{Width: 100, Height: 30},
		{Width: 40, Height: 12},
		{Width: 20, Height: 8},
	} {
		next, _ := m.Update(size)
		m = next.(*Model)
		_ = m.View()
	}
}

func TestErrorStatusStaysOnOneLine(t *testing.T) {
	m := testModel()
	m.addError(&llm.ClientError{Status: 404, ModelNotLoaded: true, Body: `model "x" is not loaded`})
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != m.height {
		t.Fatalf("view has %d lines, want %d\n%s", len(lines), m.height, view)
	}
	if !strings.Contains(view, "MODEL NOT LOADED") {
		t.Errorf("error missing from view:\n%s", view)
	}
}

func TestFormatToolArgs(t *testing.T) {
	cases := []struct {
		tool, args, want string
	}{
		{"shell", `{"command":"go test ./...","cwd":"src"}`, "$ go test ./...  (in src)"},
		{"shell", `{"command":"echo hi"}`, "$ echo hi"},
		{"search", `{"pattern":"func main","include":"*.go"}`, "/func main/ (*.go)"},
		{"file_read", `{"path":"main.go"}`, "main.go"},
		{"time", `{}`, ""},
	}
	for _, c := range cases {
		if got := formatToolArgs(c.tool, c.args); got != c.want {
			t.Errorf("formatToolArgs(%q, %q) = %q, want %q", c.tool, c.args, got, c.want)
		}
	}
}

// TestSlashMenuListsAllCommands verifies that typing a bare slash opens the
// dropdown with every command visible, and that completing the selection
// rewrites the input to the chosen command.
func TestSlashMenuListsAllCommands(t *testing.T) {
	m := testModel()
	m.width, m.height = 100, 40
	m.input.SetValue("/")
	m.suggestDismissed = false

	sugg := m.suggestions()
	if len(sugg) != len(slashCommands) {
		t.Fatalf("suggestions for \"/\" = %d, want %d (all commands)", len(sugg), len(slashCommands))
	}
	if m.suggestionLimit() < len(slashCommands) {
		t.Fatalf("suggestionLimit() = %d, want all %d commands to fit", m.suggestionLimit(), len(slashCommands))
	}

	view := m.View()
	for _, c := range slashCommands {
		if !strings.Contains(view, c.name) {
			t.Errorf("slash menu does not show %q (all 12 expected)", c.name)
		}
	}
	if !strings.Contains(view, "choose") {
		t.Errorf("menu footer hint missing")
	}

	// Select "/reset" (third entry) and complete it.
	m.suggestSel = 2
	m.completeSuggestion(m.suggestions())
	if got := m.input.Value(); got != "/reset" {
		t.Errorf("completed input = %q, want %q", got, "/reset")
	}

	// Partial prefix narrows the list. (In the app, typing a new character
	// resets suggestDismissed in handleKey; simulate that here.)
	m.input.SetValue("/st")
	m.suggestDismissed = false
	if got := m.suggestions(); len(got) != 1 || got[0].name != "/status" {
		t.Fatalf("suggestions for \"/st\" = %v, want exactly [\"/status\"]", got)
	}
}

func TestTranscriptShowsEvents(t *testing.T) {
	m := testModel()
	m.Update(AgentEventMsg(agent.Event{Kind: agent.EventStep, Step: 1}))
	m.Update(AgentEventMsg(agent.Event{Kind: agent.EventText, Text: "thinking"}))
	m.Update(AgentEventMsg(agent.Event{
		Kind: agent.EventToolCall, Tool: "file_read", Args: `{"path":"a.txt"}`,
	}))
	m.Update(AgentEventMsg(agent.Event{
		Kind: agent.EventToolResult, Tool: "file_read", Result: "ok: hello",
	}))
	m.Update(AgentEventMsg(agent.Event{Kind: agent.EventStep, Step: 2}))
	m.Update(AgentEventMsg(agent.Event{Kind: agent.EventText, Text: "final answer"}))
	m.Update(AgentEventMsg(agent.Event{Kind: agent.EventDone, Text: "final answer"}))

	content := stripANSI(m.transcript())
	for _, want := range []string{"thinking", "file_read", "ok", "hello", "final answer", "metis"} {
		if !strings.Contains(content, want) {
			t.Errorf("transcript missing %q\n%s", want, content)
		}
	}
}
