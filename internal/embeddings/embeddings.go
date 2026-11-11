// Package embeddings implements a small, cache-backed semantic search index.
//
// It talks to any OpenAI-compatible /v1/embeddings endpoint, so a local Ollama
// server with an embedding model (for example nomic-embed-text) works without a
// hosted API. The index is built lazily and cached next to the other Metis data
// files; a changed source tree rebuilds it automatically.
package embeddings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Embedder turns text into vectors. Implementations may batch internally.
type Embedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float64, error)
}

// Client calls an OpenAI-compatible embeddings endpoint.
type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// NewClient builds an embeddings client. baseURL may be a provider root, a
// URL already ending in /v1, or a full chat endpoint; the standard
// /v1/embeddings path is appended exactly once.
func NewClient(baseURL, apiKey, model string) *Client {
	return &Client{
		baseURL: normalizeBaseURL(baseURL),
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{Timeout: 120 * time.Second},
	}
}

func normalizeBaseURL(raw string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	for _, suffix := range []string{"/v1/chat/completions", "/chat/completions", "/v1/embeddings", "/v1/models"} {
		if strings.HasSuffix(base, suffix) {
			base = strings.TrimSuffix(base, suffix)
			break
		}
	}
	base = strings.TrimSuffix(base, "/v1")
	return strings.TrimRight(base, "/")
}

// Model reports the embedding model name.
func (c *Client) Model() string { return c.model }

// Embed requests vectors for inputs in batches of 32.
func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	if c.model == "" {
		return nil, errors.New("no embedding model configured")
	}
	const batch = 32
	out := make([][]float64, 0, len(inputs))
	for start := 0; start < len(inputs); start += batch {
		end := start + batch
		if end > len(inputs) {
			end = len(inputs)
		}
		vecs, err := c.embedBatch(ctx, inputs[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (c *Client) embedBatch(ctx context.Context, inputs []string) ([][]float64, error) {
	body, err := json.Marshal(map[string]any{"model": c.model, "input": inputs})
	if err != nil {
		return nil, err
	}
	url := c.baseURL + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embeddings: %s: %s", resp.Status, truncate(string(raw), 300))
	}
	var payload struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("embeddings: decode response: %w", err)
	}
	if len(payload.Data) != len(inputs) {
		return nil, fmt.Errorf("embeddings: got %d vectors for %d inputs", len(payload.Data), len(inputs))
	}
	sort.Slice(payload.Data, func(i, j int) bool { return payload.Data[i].Index < payload.Data[j].Index })
	out := make([][]float64, len(payload.Data))
	for i, d := range payload.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

// Chunk is one indexed slice of a file.
type Chunk struct {
	Path   string    `json:"path"`
	Line   int       `json:"line"`
	Text   string    `json:"text"`
	Vector []float64 `json:"vector"`
}

// Result is one search hit.
type Result struct {
	Path  string
	Line  int
	Score float64
	Text  string
}

type cacheFile struct {
	Version     int     `json:"version"`
	Model       string  `json:"model"`
	Root        string  `json:"root"`
	Fingerprint string  `json:"fingerprint"`
	Chunks      []Chunk `json:"chunks"`
}

// Index is a lazily built semantic index over one directory.
type Index struct {
	root      string
	model     string
	cacheDir  string
	embedder  Embedder
	maxFiles  int
	maxChunks int

	mu      sync.Mutex
	chunks  []Chunk
	built   bool
	builtFP string
}

// NewIndex builds an index for root. cacheDir may be empty to disable caching.
func NewIndex(root, model string, embedder Embedder, cacheDir string) *Index {
	return &Index{
		root:      root,
		model:     model,
		cacheDir:  cacheDir,
		embedder:  embedder,
		maxFiles:  400,
		maxChunks: 1500,
	}
}

// Ensure builds or loads the index. rebuild forces a fresh build.
func (ix *Index) Ensure(ctx context.Context, rebuild bool) (int, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	files, err := collectFiles(ix.root, ix.maxFiles)
	if err != nil {
		return 0, err
	}
	fp := fingerprint(files)
	if ix.built && !rebuild && fp == ix.builtFP {
		return len(ix.chunks), nil
	}
	if !rebuild {
		if chunks, ok := ix.loadCache(fp); ok {
			ix.chunks, ix.built, ix.builtFP = chunks, true, fp
			return len(chunks), nil
		}
	}

	texts := make([]string, 0, len(files))
	meta := make([]Chunk, 0, len(files))
	for _, path := range files {
		if len(meta) >= ix.maxChunks {
			break
		}
		rel, err := filepath.Rel(ix.root, path)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 512<<10 {
			continue
		}
		chunks := chunkFile(rel, string(data))
		for _, c := range chunks {
			if len(meta) >= ix.maxChunks {
				break
			}
			meta = append(meta, c)
			texts = append(texts, c.Text)
		}
	}
	if len(texts) == 0 {
		return 0, errors.New("no text files to index")
	}
	vectors, err := ix.embedder.Embed(ctx, texts)
	if err != nil {
		return 0, err
	}
	if len(vectors) != len(meta) {
		return 0, fmt.Errorf("embeddings: got %d vectors for %d chunks", len(vectors), len(meta))
	}
	for i := range meta {
		meta[i].Vector = vectors[i]
	}
	ix.chunks, ix.built, ix.builtFP = meta, true, fp
	ix.saveCache(fp)
	return len(meta), nil
}

// Search returns the best matches for query. pathPrefix, when set, limits
// results to files under that relative directory.
func (ix *Index) Search(ctx context.Context, query, pathPrefix string, max int) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("query is empty")
	}
	if max < 1 {
		max = 8
	}
	if max > 50 {
		max = 50
	}
	if _, err := ix.Ensure(ctx, false); err != nil {
		return nil, err
	}
	vec, err := ix.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vec) != 1 || len(vec[0]) == 0 {
		return nil, errors.New("embeddings: empty query vector")
	}
	prefix := strings.Trim(strings.TrimSpace(pathPrefix), "/")

	ix.mu.Lock()
	chunks := ix.chunks
	ix.mu.Unlock()

	type scored struct {
		chunk Chunk
		score float64
	}
	hits := make([]scored, 0, len(chunks))
	for _, c := range chunks {
		if prefix != "" && c.Path != prefix && !strings.HasPrefix(c.Path, prefix+"/") {
			continue
		}
		if len(c.Vector) == 0 {
			continue
		}
		hits = append(hits, scored{chunk: c, score: cosine(vec[0], c.Vector)})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score == hits[j].score {
			return hits[i].chunk.Path < hits[j].chunk.Path
		}
		return hits[i].score > hits[j].score
	})
	if len(hits) > max {
		hits = hits[:max]
	}
	out := make([]Result, 0, len(hits))
	for _, h := range hits {
		out = append(out, Result{
			Path:  h.chunk.Path,
			Line:  h.chunk.Line,
			Score: h.score,
			Text:  h.chunk.Text,
		})
	}
	return out, nil
}

// Chunks reports how many chunks are indexed (0 before Ensure).
func (ix *Index) Chunks() int {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return len(ix.chunks)
}

func (ix *Index) cachePath() string {
	if ix.cacheDir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(ix.root + "\x00" + ix.model))
	return filepath.Join(ix.cacheDir, hex.EncodeToString(sum[:8])+".json")
}

func (ix *Index) loadCache(fp string) ([]Chunk, bool) {
	path := ix.cachePath()
	if path == "" {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var c cacheFile
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, false
	}
	if c.Version != 1 || c.Model != ix.model || c.Root != ix.root || c.Fingerprint != fp {
		return nil, false
	}
	return c.Chunks, len(c.Chunks) > 0
}

func (ix *Index) saveCache(fp string) {
	path := ix.cachePath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	b, err := json.Marshal(cacheFile{
		Version:     1,
		Model:       ix.model,
		Root:        ix.root,
		Fingerprint: fp,
		Chunks:      ix.chunks,
	})
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	"vendor": true, "dist": true, "build": true, "target": true,
	".venv": true, "venv": true, "__pycache__": true, ".next": true,
	".cache": true, ".idea": true, ".vscode": true, "coverage": true,
}

func collectFiles(root string, max int) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(files) >= max {
			return fs.SkipAll
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 512<<10 || info.Size() == 0 {
			return nil
		}
		if !looksText(path) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	sort.Strings(files)
	return files, err
}

func looksText(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var buf [512]byte
	n, _ := f.Read(buf[:])
	if n == 0 {
		return false
	}
	for _, b := range buf[:n] {
		if b == 0 {
			return false
		}
		if b < 0x09 || (b > 0x0d && b < 0x20) {
			return false
		}
	}
	return true
}

func chunkFile(rel, text string) []Chunk {
	const size, overlap = 60, 8
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 1 && len(lines[0]) > 4000 {
		// Minified or single-line data: keep a bounded prefix.
		lines = []string{truncate(lines[0], 4000)}
	}
	var out []Chunk
	for start := 0; start < len(lines); start += size - overlap {
		end := start + size
		if end > len(lines) {
			end = len(lines)
		}
		body := strings.Join(lines[start:end], "\n")
		if strings.TrimSpace(body) == "" {
			continue
		}
		out = append(out, Chunk{Path: rel, Line: start + 1, Text: body})
		if end == len(lines) {
			break
		}
	}
	return out
}

func fingerprint(files []string) string {
	h := sha256.New()
	for _, p := range files {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s|%d|%d\n", p, info.Size(), info.ModTime().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil))
}

func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
