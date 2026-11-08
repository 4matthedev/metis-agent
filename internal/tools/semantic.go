package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"metis-agent/internal/embeddings"
)

// NewSemanticSearchTool exposes the cached embedding index as a model tool.
func NewSemanticSearchTool(workDir string, ix *embeddings.Index) *Tool {
	return &Tool{
		Name: "semantic_search",
		Description: "Search the repository by meaning using a local embedding index. " +
			"Use it for conceptual questions (\"where is authentication handled?\") when " +
			"regex search would be awkward. The index is built once, cached, and refreshed " +
			"automatically when files change; pass rebuild=true after large edits. " +
			"Requires an embedding model configured with --embedding-model.",
		Parameters: objectSchema(map[string]any{
			"query":       propSchema{Type: "string", Description: "Natural-language search query."},
			"path":        propSchema{Type: "string", Description: "Limit results to this directory (relative to the project)."},
			"max_results": propSchema{Type: "integer", Description: "Maximum hits to return (default 8, max 50)."},
			"rebuild":     propSchema{Type: "boolean", Description: "Force a full reindex before searching."},
		}, "query"),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			query := strings.TrimSpace(str(args["query"]))
			if query == "" {
				return "", fmt.Errorf("query is required")
			}
			prefix := ""
			if raw := strings.TrimSpace(str(args["path"])); raw != "" {
				abs, err := resolvePath(workDir, raw)
				if err != nil {
					return "", err
				}
				rel, err := filepath.Rel(workDir, abs)
				if err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
					prefix = filepath.ToSlash(rel)
				}
			}
			rebuild := boolArg(args, "rebuild")
			if rebuild {
				if _, err := ix.Ensure(ctx, true); err != nil {
					return "", describeEmbeddingError(err)
				}
			}
			max := intArg(args, "max_results", 8)
			hits, err := ix.Search(ctx, query, prefix, max)
			if err != nil {
				return "", describeEmbeddingError(err)
			}
			if len(hits) == 0 {
				return "no matches", nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%d result(s) for %q (indexed %d chunks)\n", len(hits), query, ix.Chunks())
			for i, h := range hits {
				fmt.Fprintf(&b, "\n%d. %s:%d  score=%.3f\n", i+1, h.Path, h.Line, h.Score)
				text := strings.TrimRight(h.Text, "\n")
				if len(text) > 1200 {
					text = text[:1200] + "\n…"
				}
				for _, line := range strings.Split(text, "\n") {
					b.WriteString("   ")
					b.WriteString(line)
					b.WriteByte('\n')
				}
			}
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}

func describeEmbeddingError(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "no embedding model") {
		return fmt.Errorf("semantic search needs an embedding model: pass --embedding-model NAME " +
			"(for Ollama: `ollama pull nomic-embed-text` then --embedding-model nomic-embed-text)")
	}
	if strings.Contains(msg, "404") || strings.Contains(msg, "not found") || strings.Contains(msg, "not supported") {
		return fmt.Errorf("%w (does the provider expose /v1/embeddings, and is the embedding model pulled?)", err)
	}
	return err
}
