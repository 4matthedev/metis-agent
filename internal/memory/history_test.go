package memory

import (
	"testing"

	"metis-agent/internal/llm"
)

func msg(role, content string) llm.Message {
	return llm.Message{Role: role, Content: content}
}

func toolMsg(id string) llm.Message {
	return llm.Message{Role: "tool", Content: "r", ToolCallID: id}
}

func callMsg(ids ...string) llm.Message {
	m := llm.Message{Role: "assistant"}
	for _, id := range ids {
		m.ToolCalls = append(m.ToolCalls, llm.ToolCall{ID: id, Type: "function"})
	}
	return m
}

func TestMessagesUnderWindowUntouched(t *testing.T) {
	h := NewHistory(10)
	h.Append(msg("user", "hi"))
	h.Append(msg("assistant", "hello"))
	got := h.Messages()
	if len(got) != 2 || got[0].Content != "hi" || got[1].Content != "hello" {
		t.Fatalf("unexpected messages: %+v", got)
	}
}

func TestMessagesDropsLeadingOrphanTool(t *testing.T) {
	h := NewHistory(2)
	h.Append(msg("user", "hi"))
	h.Append(callMsg("c1"))
	h.Append(toolMsg("c1"))
	h.Append(msg("assistant", "done"))

	got := h.Messages()
	if len(got) != 1 || got[0].Content != "done" {
		t.Fatalf("messages = %+v, want just the final assistant message", got)
	}
}

func TestMessagesDropsIncompleteToolGroup(t *testing.T) {
	h := NewHistory(3)
	h.Append(msg("user", "hi"))
	h.Append(callMsg("c1", "c2"))
	h.Append(toolMsg("c1")) // c2's result is outside the window
	h.Append(msg("assistant", "final"))

	got := h.Messages()
	// Window is [call, tool(c1), final]; the call is incomplete, but the
	// final assistant message is still valid on its own.
	if len(got) != 1 || got[0].Content != "final" {
		t.Fatalf("messages = %+v, want just the final message", got)
	}
}

func TestMessagesDropsTrailingAssistantCall(t *testing.T) {
	h := NewHistory(2)
	h.Append(msg("user", "hi"))
	h.Append(msg("assistant", "thinking"))
	h.Append(callMsg("c1"))

	got := h.Messages()
	if len(got) != 1 || got[0].Content != "thinking" {
		t.Fatalf("messages = %+v, want the plain assistant message", got)
	}
}

func TestReset(t *testing.T) {
	h := NewHistory(4)
	h.Append(msg("user", "hi"))
	h.Reset()
	if h.Len() != 0 {
		t.Fatalf("Len = %d, want 0", h.Len())
	}
	if got := h.Messages(); len(got) != 0 {
		t.Fatalf("messages = %+v, want empty", got)
	}
}

func TestTruncate(t *testing.T) {
	h := NewHistory(8)
	h.Append(msg("user", "one"))
	h.Append(msg("assistant", "two"))
	h.Append(msg("user", "three"))
	h.Truncate(2)
	if h.Len() != 2 {
		t.Fatalf("Len = %d, want 2", h.Len())
	}
	h.Truncate(0)
	if h.Len() != 0 {
		t.Fatalf("Len = %d, want 0", h.Len())
	}
	h.Truncate(-1) // must not panic
}
