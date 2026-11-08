package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouterFallsBackAndSwitches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Model string `json:"model"`
			ThinkingLevel string `json:"reasoning_effort"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		if body.Model != "good" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"model not found"}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	r, err := NewRouter("compatible", srv.URL, "", "bad", 0, ThinkingHigh, []string{"good"})
	if err != nil {
		t.Fatal(err)
	}
	var switched string
	r.SetSwitchCallback(func(m string) { switched = m })
	comp, err := r.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if comp.Content != "hi" {
		t.Fatalf("content = %q", comp.Content)
	}
	if switched != "good" || r.Model() != "good" {
		t.Fatalf("switch callback=%q model=%q", switched, r.Model())
	}
}

func TestSetModel(t *testing.T) {
	r, err := NewRouter("compatible", "http://127.0.0.1:1", "", "a", 0, ThinkingNone, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetModel("b"); err != nil {
		t.Fatal(err)
	}
	if r.Model() != "b" {
		t.Fatalf("model = %q", r.Model())
	}
}

func TestSetThinking(t *testing.T) {
	r, _ := NewRouter("compatible", "http://127.0.0.1:1", "", "a", 0, ThinkingNone, nil)
	if r.Thinking() != ThinkingNone {
		t.Fatalf("initial thinking = %q, want none", r.Thinking())
	}
	r.SetThinking(ThinkingHigh)
	if r.Thinking() != ThinkingHigh {
		t.Fatalf("after SetThinking = %q, want high", r.Thinking())
	}
}
