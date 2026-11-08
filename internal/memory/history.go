// Package memory keeps the conversation history with a sliding window, so
// the prompt sent to the model stays bounded.
package memory

import (
	"strings"

	"metis-agent/internal/llm"
)

// History is an append-only log of messages; Messages() returns the most
// recent window of them in a shape the chat API accepts.
type History struct {
	messages []llm.Message
	window   int
}

// NewHistory returns an empty history that keeps at most window messages.
func NewHistory(window int) *History {
	if window <= 0 {
		window = 24
	}
	return &History{window: window}
}

// Append adds a message to the history.
func (h *History) Append(m llm.Message) {
	h.messages = append(h.messages, m)
}

// Reset drops all messages.
func (h *History) Reset() {
	h.messages = nil
}

// Len returns the number of stored messages.
func (h *History) Len() int { return len(h.messages) }

// All returns a copy of the full history, ignoring the sliding window.
func (h *History) All() []llm.Message {
	return cloneMessages(h.messages)
}

// Load replaces the stored history with msgs after dropping any message that
// would produce an invalid conversation. This is used when resuming a saved
// session: an interrupted run may have left an assistant tool call without its
// results, which model servers would reject.
func (h *History) Load(msgs []llm.Message) {
	h.messages = sanitizeMessages(msgs)
}

// sanitizeMessages removes system messages, orphaned tool results, and
// incomplete tool-call groups. Empty turns are dropped as well.
func sanitizeMessages(in []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(in))
	for i := 0; i < len(in); {
		m := in[i]
		switch m.Role {
		case "system":
			// The agent always prepends its own system prompt.
			i++

		case "tool":
			// An orphaned result; the matching assistant call was dropped.
			i++

		case "assistant":
			if len(m.ToolCalls) == 0 {
				if strings.TrimSpace(m.Content) != "" {
					out = append(out, cloneMessage(m))
				}
				i++
				continue
			}
			// Collect the results that follow this call.
			j := i + 1
			have := make(map[string]bool, len(m.ToolCalls))
			for j < len(in) && in[j].Role == "tool" {
				have[in[j].ToolCallID] = true
				j++
			}
			complete := true
			for _, tc := range m.ToolCalls {
				if tc.ID == "" || !have[tc.ID] {
					complete = false
					break
				}
			}
			if !complete {
				// Drop the call and all of its partial results.
				i = j
				continue
			}
			out = append(out, cloneMessage(m))
			for k := i + 1; k < j; k++ {
				out = append(out, cloneMessage(in[k]))
			}
			i = j

		case "user":
			if strings.TrimSpace(m.Content) != "" {
				out = append(out, cloneMessage(m))
			}
			i++

		default:
			i++
		}
	}
	return out
}

func cloneMessages(in []llm.Message) []llm.Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]llm.Message, len(in))
	for i, m := range in {
		out[i] = cloneMessage(m)
	}
	return out
}

func cloneMessage(m llm.Message) llm.Message {
	if len(m.ToolCalls) > 0 {
		m.ToolCalls = append([]llm.ToolCall(nil), m.ToolCalls...)
	}
	return m
}

// Truncate keeps only the first n messages.
func (h *History) Truncate(n int) {
	if n < 0 {
		n = 0
	}
	if n < len(h.messages) {
		h.messages = h.messages[:n]
	}
}

// Messages returns the most recent window of the history. It is trimmed so
// that the result is always a well-formed OpenAI conversation:
//
//   - it never starts with an orphaned tool result, and
//   - an assistant tool-call message is never left without all of its tool
//     results (which would be rejected by the server).
//
// When trimming would break a tool-call group, the whole group is dropped.
func (h *History) Messages() []llm.Message {
	m := h.messages
	if len(m) <= h.window {
		return m
	}
	m = m[len(m)-h.window:]
	m = append([]llm.Message(nil), m...) // own the slice; trimming below mutates it

	// Never start in the middle of a tool-call group.
	for len(m) > 0 && m[0].Role == "tool" {
		m = m[1:]
	}

	// Drop any tool-call group that lost some of its results to trimming.
	// Only the first such group can be incomplete (the window always ends
	// at the newest message), but loop in case dropping it exposes another.
	for len(m) > 0 {
		idx := -1
		for i, msg := range m {
			if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		end := idx + 1
		have := map[string]bool{}
		for end < len(m) && m[end].Role == "tool" {
			have[m[end].ToolCallID] = true
			end++
		}
		complete := true
		for _, tc := range m[idx].ToolCalls {
			if !have[tc.ID] {
				complete = false
				break
			}
		}
		if complete {
			break
		}
		m = append(m[:idx], m[end:]...)
		for len(m) > 0 && m[0].Role == "tool" {
			m = m[1:]
		}
	}

	return cloneMessages(m)
}
