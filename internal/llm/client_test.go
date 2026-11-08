package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCompleteParsesStreamedToolCalls(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Let me "},"index":0}]}`,
		`{"choices":[{"delta":{"content":"check."}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"file_read","arguments":"{\"pa"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a.txt\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			fl.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	var streamed strings.Builder
	c := NewClient(srv.URL, "test-model", 0.1)
	comp, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, func(s string) {
		streamed.WriteString(s)
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got, want := comp.Content, "Let me check."; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
	if got, want := streamed.String(), "Let me check."; got != want {
		t.Errorf("streamed = %q, want %q", got, want)
	}
	if len(comp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(comp.ToolCalls))
	}
	tc := comp.ToolCalls[0]
	if tc.Function.Name != "file_read" {
		t.Errorf("tool name = %q, want file_read", tc.Function.Name)
	}
	if got, want := tc.Function.Arguments, `{"path":"a.txt"}`; got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
	if tc.ID != "call_1" {
		t.Errorf("id = %q, want call_1", tc.ID)
	}
	if comp.FinishReason != "tool_calls" {
		t.Errorf("finish reason = %q, want tool_calls", comp.FinishReason)
	}
}

func TestCompleteNonStreamingJSONFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hello","tool_calls":[{"id":"x","type":"function","function":{"name":"time","arguments":"{}"}}]},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "m", 0)
	comp, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if comp.Content != "hello" {
		t.Errorf("content = %q, want hello", comp.Content)
	}
	if len(comp.ToolCalls) != 1 || comp.ToolCalls[0].Function.Name != "time" {
		t.Errorf("unexpected tool calls: %+v", comp.ToolCalls)
	}
}

func TestModelNotLoaded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":"model Qwen is not loaded"}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "Qwen", 0)
	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsModelNotLoaded(err) {
		t.Errorf("IsModelNotLoaded = false for %v", err)
	}
}

func TestInvalidToolArgumentsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c\",\"function\":{\"name\":\"x\",\"arguments\":\"{nope\"}}]}}]}\n\n")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "m", 0)
	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid tool call arguments") {
		t.Fatalf("err = %v, want invalid arguments error", err)
	}
}

// TestStalledStreamIsAborted verifies the idle watchdog: a stream that stops
// sending bytes is cancelled with a clear error instead of hanging forever or
// surfacing a bare context.Canceled.
func TestStalledStreamIsAborted(t *testing.T) {
	old := streamIdleTimeout
	streamIdleTimeout = 60 * time.Millisecond
	t.Cleanup(func() { streamIdleTimeout = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done() // hold the connection open until the client gives up
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-model", 0.1)
	start := time.Now()
	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err == nil {
		t.Fatal("expected the stalled stream to fail")
	}
	if !strings.Contains(err.Error(), "stream stalled") {
		t.Fatalf("err = %v, want a stalled-stream message", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("watchdog took %s, want it to fire near the configured timeout", elapsed)
	}
}

// TestSlowSteadyStreamSurvives documents the point of an idle timeout: a stream
// whose total duration exceeds the idle window is fine as long as bytes keep
// arriving.
func TestSlowSteadyStreamSurvives(t *testing.T) {
	old := streamIdleTimeout
	streamIdleTimeout = 120 * time.Millisecond
	t.Cleanup(func() { streamIdleTimeout = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, word := range []string{"slow", " but", " steady"} {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", word)
			fl.Flush()
			time.Sleep(40 * time.Millisecond) // total > idle window, each gap < it
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-model", 0.1)
	comp, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("steady stream was killed: %v", err)
	}
	if comp.Content != "slow but steady" {
		t.Fatalf("content = %q", comp.Content)
	}
}
