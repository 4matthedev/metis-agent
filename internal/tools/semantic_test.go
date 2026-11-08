package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metis-agent/internal/embeddings"
)

type wordEmbedder struct{ words []string }

func (e *wordEmbedder) Embed(_ context.Context, inputs []string) ([][]float64, error) {
	out := make([][]float64, len(inputs))
	for i, s := range inputs {
		vec := make([]float64, len(e.words))
		low := strings.ToLower(s)
		for j, w := range e.words {
			vec[j] = float64(strings.Count(low, w))
		}
		out[i] = vec
	}
	return out, nil
}

func TestSemanticSearchTool(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"auth.go":       "func login() { /* authenticate the session token */ }",
		"database.go":   "func query() { /* database connection */ }",
		"components.md": "render the user interface",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	emb := &wordEmbedder{words: []string{"authenticate", "session", "database", "render"}}
	ix := embeddings.NewIndex(root, "test", emb, t.TempDir())
	tool := NewSemanticSearchTool(root, ix)
	reg := NewRegistryWithOptions(Options{WorkDir: root})
	reg.Add(tool)

	out := reg.Execute(context.Background(), "semantic_search", []byte(`{"query":"authenticate session","max_results":3}`))
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("semantic_search failed: %s", out)
	}
	if !strings.Contains(out, "auth.go") {
		t.Fatalf("auth.go missing from results:\n%s", out)
	}
	if !strings.Contains(out, "score=") {
		t.Fatalf("missing scores:\n%s", out)
	}

	out = reg.Execute(context.Background(), "semantic_search", []byte(`{"query":"database","path":"database.go"}`))
	if strings.HasPrefix(out, "error:") || !strings.Contains(out, "database.go") {
		t.Fatalf("path-filtered search = %s", out)
	}
}
