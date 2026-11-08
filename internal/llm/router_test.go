package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouterFallsBackAndSwitches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Model string `json:"model"`
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

	r, err := NewRouter("compatible", srv.URL, "", "bad", 0, []string{"good"})
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

func TestRouterReturnsErrorWhenAllFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()
	r, err := NewRouter("compatible", srv.URL, "", "a", 0, []string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "all models failed") {
		t.Fatalf("err = %v, want all models failed", err)
	}
}

func TestSetModel(t *testing.T) {
	r, err := NewRouter("compatible", "http://127.0.0.1:1", "", "a", 0, nil)
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
