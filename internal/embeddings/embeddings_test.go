package embeddings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// keywordEmbedder maps a text to a tiny bag-of-words vector so ranking is
// deterministic in tests.
type keywordEmbedder struct {
	words  []string
	calls  int
	failOn int
}

func (e *keywordEmbedder) Embed(_ context.Context, inputs []string) ([][]float64, error) {
	e.calls++
	if e.failOn > 0 && e.calls >= e.failOn {
		return nil, errors.New("embedder should not have been called")
	}
	out := make([][]float64, len(inputs))
	for i, s := range inputs {
		low := strings.ToLower(s)
		vec := make([]float64, len(e.words))
		for j, w := range e.words {
			vec[j] = float64(strings.Count(low, w))
		}
		out[i] = vec
	}
	return out, nil
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIndexBuildAndSearch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "auth/session.go", "func login(user string) {\n\t// authenticate the session token\n}\n")
	writeFile(t, root, "db/query.go", "func query() {\n\t// database connection and SQL\n}\n")
	writeFile(t, root, "ui/view.go", "func render() {\n\t// draw the interface\n}\n")
	writeFile(t, root, "node_modules/ignored.js", "authenticate session token")

	emb := &keywordEmbedder{words: []string{"authenticate", "session", "database", "render"}}
	ix := NewIndex(root, "test-model", emb, t.TempDir())
	n, err := ix.Ensure(context.Background(), false)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if n == 0 {
		t.Fatal("index is empty")
	}
	hits, err := ix.Search(context.Background(), "authenticate session", "", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Path != "auth/session.go" {
		t.Fatalf("top hit = %q, want auth/session.go (%#v)", hits[0].Path, hits)
	}
	for _, h := range hits {
		if strings.Contains(h.Path, "node_modules") {
			t.Fatalf("indexed an ignored directory: %q", h.Path)
		}
	}

	// A path prefix filters results.
	dbHits, err := ix.Search(context.Background(), "database", "db", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(dbHits) != 1 || dbHits[0].Path != "db/query.go" {
		t.Fatalf("prefix search = %#v", dbHits)
	}
}

func TestIndexUsesCache(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "authentication session handling")
	cache := t.TempDir()

	first := &keywordEmbedder{words: []string{"authentication", "session"}}
	ix := NewIndex(root, "m", first, cache)
	if _, err := ix.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if first.calls != 1 {
		t.Fatalf("first build calls = %d", first.calls)
	}

	// Reopening the same index must reuse the cache instead of embedding again.
	second := &keywordEmbedder{words: []string{"authentication", "session"}, failOn: 1}
	ix2 := NewIndex(root, "m", second, cache)
	if _, err := ix2.Ensure(context.Background(), false); err != nil {
		t.Fatalf("cached Ensure: %v", err)
	}
	if second.calls != 0 {
		t.Fatalf("cache was not used: %d embedder calls", second.calls)
	}

	// Changing the tree invalidates the cache and forces a rebuild.
	writeFile(t, root, "b.txt", "database connection")
	third := &keywordEmbedder{words: []string{"authentication", "session", "database"}}
	ix3 := NewIndex(root, "m", third, cache)
	if _, err := ix3.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if third.calls == 0 {
		t.Fatal("changed tree did not trigger a rebuild")
	}
}

func TestClientEmbed(t *testing.T) {
	var gotModel string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %q", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		gotModel = body.Model
		data := make([]any, len(body.Input))
		for i := range body.Input {
			data[i] = map[string]any{"index": i, "embedding": []float64{float64(i), 1}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "secret", "embed-model")
	vecs, err := c.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || len(vecs[1]) != 2 || vecs[1][0] != 1 {
		t.Fatalf("vectors = %#v", vecs)
	}
	if gotModel != "embed-model" {
		t.Fatalf("model = %q", gotModel)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("authorization = %q", gotAuth)
	}
}

func TestClientRejectsBadResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such model", http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "", "missing")
	if _, err := c.Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected an error")
	}
}
