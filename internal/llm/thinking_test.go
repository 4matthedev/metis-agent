package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompleteSendsReasoningEffort(t *testing.T) {
	var gotReasoning string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model         string
			ReasoningEffort string `json:"reasoning_effort"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotReasoning = body.ReasoningEffort
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-model", 0.3)
	c.Thinking = ThinkingHigh
	_, err := c.Complete(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if gotReasoning != "high" {
		t.Fatalf("reasoning_effort = %q, want high", gotReasoning)
	}

	// With ThinkingNone, the param is omitted (omitempty).
	c.Thinking = ThinkingNone
	_, err = c.Complete(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if gotReasoning != "" {
		t.Fatalf("reasoning_effort with ThinkingNone = %q, want empty", gotReasoning)
	}
}
