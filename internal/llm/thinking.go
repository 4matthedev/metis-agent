// Thinking level (reasoning effort) for OpenAI-compatible servers. Metis sends
// this as the `reasoning_effort` parameter on every chat completion. Servers that
// don't understand it (older Ollama, local llama.cpp builds) ignore it; servers
// that do (OpenAI o-series, Claude via OpenRouter, DeepSeek R1, Qwen3) honor it.
//
// "none" is the default — nothing is sent, so the server uses its own default.

package llm

import (
	"fmt"
	"strings"
)

// ThinkingLevel is a user-facing knob for how much the model may "think"
// (reason internally) before answering. The exact token budget is a server
// decision; Metis just passes the standard effort name.
type ThinkingLevel string

const (
	// ThinkingNone sends no reasoning_effort parameter. The server picks its
	// own default. This is the safe default for local servers.
	ThinkingNone ThinkingLevel = ""

	// ThinkingLow requests minimal reasoning.
	ThinkingLow ThinkingLevel = "low"

	// ThinkingMedium requests moderate reasoning.
	ThinkingMedium ThinkingLevel = "medium"

	// ThinkingHigh requests extended reasoning.
	ThinkingHigh ThinkingLevel = "high"

	// ThinkingXHigh requests maximum available reasoning.
	ThinkingXHigh ThinkingLevel = "xhigh"
)

// ParseThinkingLevel converts a user-supplied string into a ThinkingLevel.
// Empty input yields ThinkingNone. Unknown input returns an error so a typo
// does not silently turn reasoning off.
func ParseThinkingLevel(s string) (ThinkingLevel, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "", "none", "off", "0":
		return ThinkingNone, nil
	case "low", "1":
		return ThinkingLow, nil
	case "medium", "2":
		return ThinkingMedium, nil
	case "high", "3":
		return ThinkingHigh, nil
	case "xhigh", "4", "max":
		return ThinkingXHigh, nil
	}
	return ThinkingNone, fmt.Errorf("thinking must be none, low, medium, high, or xhigh (got %q)", s)
}

// ReasoningEffort returns the JSON key value to include in the request, or
// "" when no value should be sent (ThinkingNone).
func (l ThinkingLevel) ReasoningEffort() string {
	switch l {
	case ThinkingLow:
		return "low"
	case ThinkingMedium:
		return "medium"
	case ThinkingHigh:
		return "high"
	case ThinkingXHigh:
		return "xhigh"
	default:
		return ""
	}
}

// String returns a stable display name for the level.
func (l ThinkingLevel) String() string {
	switch l {
	case ThinkingLow:
		return "low"
	case ThinkingMedium:
		return "medium"
	case ThinkingHigh:
		return "high"
	case ThinkingXHigh:
		return "xhigh"
	default:
		return "none"
	}
}

// Levels returns the list of levels in display order (used by /thinking).
func Levels() []ThinkingLevel {
	return []ThinkingLevel{
		ThinkingNone,
		ThinkingLow,
		ThinkingMedium,
		ThinkingHigh,
		ThinkingXHigh,
	}
}

// ValidThinkingLevel is a helper for validation in config.
func ValidThinkingLevel(s string) bool {
	_, err := ParseThinkingLevel(s)
	return err == nil
}
