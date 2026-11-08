package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"metis-agent/internal/llm"
	"metis-agent/internal/tools"
)

type fakeCompleter struct {
	responses []*llm.Completion
	errs      []error
	calls     int
	prompts   [][]llm.Message
}

func (f *fakeCompleter) Complete(_ context.Context, messages []llm.Message, _ []llm.Tool, onToken func(string)) (*llm.Completion, error) {
	cp := make([]llm.Message, len(messages))
	copy(cp, messages)
	f.prompts = append(f.prompts, cp)
	i := f.calls
	f.calls++
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	if i >= len(f.responses) {
		return &llm.Completion{Content: "fallback"}, nil
	}
	r := f.responses[i]
	if onToken != nil && r.Content != "" {
		onToken(r.Content)
	}
	return r, nil
}

func newTestAgent(t *testing.T, fc *fakeCompleter, maxSteps int) (*Agent, *tools.Registry) {
	t.Helper()
	reg := tools.NewRegistry(t.TempDir())
	reg.Add(&tools.Tool{
		Name:        "echo",
		Description: "echo a value",
		Parameters:  map[string]any{"type": "object"},
		Handler: func(context.Context, map[string]any) (string, error) {
			return "tool-result", nil
		},
	})
	a := New(Config{LLM: fc, Registry: reg, System: "sys", MaxSteps: maxSteps, Window: 32})
	return a, reg
}

func TestRunExecutesToolThenAnswers(t *testing.T) {
	fc := &fakeCompleter{responses: []*llm.Completion{
		{
			Content:   "checking",
			ToolCalls: []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.ToolFunctionWire{Name: "echo", Arguments: `{"x":1}`}}},
		},
		{Content: "done"},
	}}
	a, _ := newTestAgent(t, fc, 5)

	var kinds []EventKind
	var text strings.Builder
	a.SetEventSink(func(ev Event) {
		kinds = append(kinds, ev.Kind)
		if ev.Kind == EventText {
			text.WriteString(ev.Text)
		}
	})

	answer, err := a.Run(context.Background(), "do it")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "done" {
		t.Errorf("answer = %q, want done", answer)
	}
	if got := text.String(); got != "checkingdone" {
		t.Errorf("streamed text = %q, want checkingdone", got)
	}
	want := []EventKind{EventStep, EventText, EventToolCall, EventToolResult, EventStep, EventText, EventDone}
	if len(kinds) != len(want) {
		t.Fatalf("events = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("event[%d] = %v, want %v", i, kinds[i], want[i])
		}
	}

	// The second prompt must contain the assistant tool call and its result.
	if len(fc.prompts) != 2 {
		t.Fatalf("model calls = %d, want 2", len(fc.prompts))
	}
	second := fc.prompts[1]
	var sawTool bool
	for _, msg := range second {
		if msg.Role == "tool" && msg.Content == "tool-result" && msg.ToolCallID == "c1" {
			sawTool = true
		}
	}
	if !sawTool {
		t.Errorf("tool result missing from follow-up prompt: %+v", second)
	}
}

func TestRunKeepsConversation(t *testing.T) {
	fc := &fakeCompleter{responses: []*llm.Completion{{Content: "first"}, {Content: "second"}}}
	a, _ := newTestAgent(t, fc, 3)

	if _, err := a.Run(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Run(context.Background(), "two"); err != nil {
		t.Fatal(err)
	}
	second := fc.prompts[1]
	var users []string
	for _, m := range second {
		if m.Role == "user" {
			users = append(users, m.Content)
		}
	}
	if len(users) != 2 || users[0] != "one" || users[1] != "two" {
		t.Errorf("users in second prompt = %v, want [one two]", users)
	}

	a.Reset()
	if _, err := a.Run(context.Background(), "three"); err != nil {
		t.Fatal(err)
	}
	for _, m := range fc.prompts[2] {
		if m.Role == "user" && m.Content == "one" {
			t.Error("history was not reset")
		}
	}
}

func TestRunMaxSteps(t *testing.T) {
	call := &llm.Completion{
		ToolCalls: []llm.ToolCall{{ID: "c", Type: "function", Function: llm.ToolFunctionWire{Name: "echo", Arguments: `{}`}}},
	}
	fc := &fakeCompleter{responses: []*llm.Completion{call, call, call}}
	a, _ := newTestAgent(t, fc, 2)

	_, err := a.Run(context.Background(), "loop")
	if !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("err = %v, want ErrMaxSteps", err)
	}
	if a.hist.Len() != 0 {
		t.Errorf("history was not rolled back after max steps: %d messages", a.hist.Len())
	}
}

func TestRunRollsBackFailedTurn(t *testing.T) {
	call := &llm.Completion{
		ToolCalls: []llm.ToolCall{{ID: "c", Type: "function", Function: llm.ToolFunctionWire{Name: "echo", Arguments: `{}`}}},
	}
	fc := &fakeCompleter{
		responses: []*llm.Completion{call, {Content: "ok"}},
		errs:      []error{nil, errors.New("boom")},
	}
	a, _ := newTestAgent(t, fc, 3)

	if _, err := a.Run(context.Background(), "bad"); err == nil {
		t.Fatal("expected the first run to fail")
	}
	if a.hist.Len() != 0 {
		t.Fatalf("history after failed run = %d messages, want 0", a.hist.Len())
	}
	if _, err := a.Run(context.Background(), "good"); err != nil {
		t.Fatal(err)
	}
	for _, m := range fc.prompts[len(fc.prompts)-1] {
		if m.Content == "bad" {
			t.Error("failed turn leaked into the next prompt")
		}
	}
}

func TestRunErrorEvent(t *testing.T) {
	fc := &fakeCompleter{errs: []error{errors.New("boom")}}
	a, _ := newTestAgent(t, fc, 2)
	var got error
	a.SetEventSink(func(ev Event) {
		if ev.Kind == EventError {
			got = ev.Err
		}
	})
	if _, err := a.Run(context.Background(), "x"); err == nil {
		t.Fatal("expected error")
	}
	if got == nil || got.Error() != "boom" {
		t.Errorf("error event = %v, want boom", got)
	}
}

func TestRunCancelsToolContext(t *testing.T) {
	call := &llm.Completion{
		ToolCalls: []llm.ToolCall{{ID: "c", Type: "function", Function: llm.ToolFunctionWire{Name: "block", Arguments: `{}`}}},
	}
	fc := &fakeCompleter{responses: []*llm.Completion{call, {Content: "unreachable"}}}
	reg := tools.NewRegistry(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var sawDone bool
	reg.Add(&tools.Tool{
		Name:       "block",
		Parameters: map[string]any{"type": "object"},
		Handler: func(hctx context.Context, _ map[string]any) (string, error) {
			cancel()
			<-hctx.Done()
			sawDone = true
			return "", hctx.Err()
		},
	})
	a := New(Config{LLM: fc, Registry: reg, System: "sys", MaxSteps: 5, Window: 16})

	if _, err := a.Run(ctx, "cancel it"); err == nil {
		t.Fatal("expected cancellation error")
	}
	if !sawDone {
		t.Fatal("tool handler did not observe cancellation")
	}
	if a.hist.Len() != 0 {
		t.Errorf("history after cancellation = %d, want 0", a.hist.Len())
	}
}

// TestModeSwitchingChangesSystemPrompt verifies that the effective system
// prompt changes with the mode.
func TestModeSwitchingChangesSystemPrompt(t *testing.T) {
	a, _ := newTestAgent(t, &fakeCompleter{responses: nil}, 5)
	// General: base prompt only, no non-general mode tag.
	general := a.System()
	for _, tag := range []string{"PLANNER mode", "BUILDER mode", "DEBUGGER mode",
		"CYBERSECURITY mode", "SIMPLIFIER mode"} {
		if strings.Contains(general, tag) {
			t.Fatalf("general prompt contains non-general mode tag %q", tag)
		}
	}

	a.SetMode(ModePlanner)
	if !strings.Contains(a.System(), "PLANNER mode") {
		t.Fatalf("planner prompt missing 'PLANNER mode'")
	}

	a.SetMode(ModeBuilder)
	if !strings.Contains(a.System(), "BUILDER mode") {
		t.Fatalf("builder prompt missing 'BUILDER mode'")
	}

	a.SetMode(ModeDebugger)
	if !strings.Contains(a.System(), "DEBUGGER mode") {
		t.Fatalf("debugger prompt missing 'DEBUGGER mode'")
	}

	a.SetMode(ModeCybersecurity)
	if !strings.Contains(a.System(), "CYBERSECURITY mode") {
		t.Fatalf("cybersecurity prompt missing 'CYBERSECURITY mode'")
	}

	a.SetMode(ModeSimplifier)
	if !strings.Contains(a.System(), "SIMPLIFIER mode") {
		t.Fatalf("simplifier prompt missing 'SIMPLIFIER mode'")
	}

	// Out-of-range modes clamp to General (no mode tag).
	a.SetMode(Mode(99))
	for _, tag := range []string{"PLANNER mode", "BUILDER mode", "DEBUGGER mode",
		"CYBERSECURITY mode", "SIMPLIFIER mode"} {
		if strings.Contains(a.System(), tag) {
			t.Errorf("clamped mode should be general, but prompt contains %q", tag)
		}
	}
}

// TestModeNameAndDescription verifies the human-friendly helpers.
func TestModeNameAndDescription(t *testing.T) {
	if ModeName(ModeGeneral) != "General" {
		t.Errorf("ModeName(Generic) = %q, want General", ModeName(ModeGeneral))
	}
	if ModeName(ModeCybersecurity) != "Cybersecurity" {
		t.Errorf("ModeName(Cybersecurity) = %q, want Cybersecurity", ModeName(ModeCybersecurity))
	}
	if ModeName(Mode(42)) != "General" {
		t.Errorf("ModeName(out of range) = %q, want General", ModeName(Mode(42)))
	}
	d := ModeDescription(ModeDebugger)
	if d == "" {
		t.Fatalf("ModeDescription(Debugger) empty")
	}
}

// TestRunUsesModeSection verifies the mode section is actually sent to the
// model on every turn, not just reported via System().
func TestRunUsesModeSection(t *testing.T) {
	fc := &fakeCompleter{responses: []*llm.Completion{{Content: "ok"}}}
	a, _ := newTestAgent(t, fc, 3)
	a.SetMode(ModePlanner)
	a.Run(context.Background(), "hi")

	if len(fc.prompts) != 1 {
		t.Fatalf("model calls = %d, want 1", len(fc.prompts))
	}
	second := fc.prompts[0]
	var system string
	for _, msg := range second {
		if msg.Role == "system" {
			system = msg.Content
		}
	}
	if system == "" {
		t.Fatalf("no system message in prompt")
	}
	if !strings.Contains(system, "PLANNER mode") {
		t.Errorf("system prompt sent to model missing 'PLANNER mode': mode was not applied")
	}
}
