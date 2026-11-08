package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoMapFindsSymbols(t *testing.T) {
	dir := t.TempDir()
	src := `package demo

type Server struct{}

func NewServer() *Server { return &Server{} }

func (s *Server) Start() error { return nil }
`
	if err := os.WriteFile(filepath.Join(dir, "server.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewRepoMapTool(dir)
	out, err := tool.Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"type Server (line 3)", "func NewServer (line 5)", "func Start (line 7)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("repo map missing %q:\n%s", want, out)
		}
	}
}
