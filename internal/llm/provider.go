package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Provider describes one OpenAI-compatible backend preset.
type Provider struct {
	Name      string
	BaseURL   string
	APIKeyEnv string
	Local     bool
}

var providerPresets = map[string]Provider{
	"llama-prism": {
		Name:    "llama-prism",
		BaseURL: "http://127.0.0.1:9931",
		Local:   true,
	},
	"lmstudio": {
		Name:      "lmstudio",
		BaseURL:   "http://127.0.0.1:1234",
		APIKeyEnv: "LMSTUDIO_API_KEY",
		Local:     true,
	},
	"ollama": {
		Name:      "ollama",
		BaseURL:   "http://127.0.0.1:11434",
		APIKeyEnv: "OLLAMA_API_KEY",
		Local:     true,
	},
	"openai": {
		Name:      "openai",
		BaseURL:   "https://api.openai.com",
		APIKeyEnv: "OPENAI_API_KEY",
	},
	"openrouter": {
		Name:      "openrouter",
		BaseURL:   "https://openrouter.ai/api",
		APIKeyEnv: "OPENROUTER_API_KEY",
	},
	"groq": {
		Name:      "groq",
		BaseURL:   "https://api.groq.com/openai",
		APIKeyEnv: "GROQ_API_KEY",
	},
	"together": {
		Name:      "together",
		BaseURL:   "https://api.together.xyz",
		APIKeyEnv: "TOGETHER_API_KEY",
	},
	"compatible": {
		Name: "compatible",
	},
}

// ProviderNames returns all provider names in a stable order.
func ProviderNames() []string {
	names := make([]string, 0, len(providerPresets))
	for name := range providerPresets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// LocalProviderNames returns the local providers probed by auto-detection.
func LocalProviderNames() []string {
	return []string{"llama-prism", "lmstudio", "ollama"}
}

func normalizeProviderName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "", "auto":
		return "auto"
	case "llama_prism", "llamaprism", "prism":
		return "llama-prism"
	case "lm-studio", "lm_studio":
		return "lmstudio"
	case "openai-compatible", "openai_compatible", "custom":
		return "compatible"
	}
	return n
}

func lookupProvider(name string) (Provider, bool) {
	p, ok := providerPresets[normalizeProviderName(name)]
	return p, ok
}

func resolveProviderAPIKey(p Provider, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if p.APIKeyEnv != "" {
		if v := strings.TrimSpace(os.Getenv(p.APIKeyEnv)); v != "" {
			return v
		}
	}
	return strings.TrimSpace(os.Getenv("METIS_API_KEY"))
}

// endpointURLs accepts a host URL, a URL with /v1, or a full chat URL and
// returns the standard OpenAI-compatible endpoints.
func endpointURLs(base string) (chatURL, modelsURL, displayBase string) {
	b := strings.TrimSpace(base)
	b = strings.TrimRight(b, "/")
	for _, suffix := range []string{"/v1/chat/completions", "/chat/completions"} {
		if strings.HasSuffix(b, suffix) {
			b = strings.TrimSuffix(b, suffix)
			break
		}
	}
	b = strings.TrimSuffix(b, "/v1")
	b = strings.TrimRight(b, "/")
	return b + "/v1/chat/completions", b + "/v1/models", b
}

// ModelInfo is one entry from a provider's model list.
type ModelInfo struct {
	ID     string
	Loaded bool
	Status string
}

// ListModels fetches the provider's model catalog.
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ModelsURL, nil)
	if err != nil {
		return nil, &ClientError{Cause: err}
	}
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &ClientError{Cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound && c.Provider == "ollama" {
		return c.listOllamaTags(ctx)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, &ClientError{Status: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}

	var payload struct {
		Data []struct {
			ID     string `json:"id"`
			Status struct {
				Value string `json:"value"`
			} `json:"status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode model list: %w", err)
	}
	out := make([]ModelInfo, 0, len(payload.Data))
	for _, m := range payload.Data {
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		info := ModelInfo{ID: m.ID, Loaded: true, Status: strings.TrimSpace(m.Status.Value)}
		if info.Status != "" {
			info.Loaded = strings.EqualFold(info.Status, "loaded")
		}
		out = append(out, info)
	}
	return out, nil
}

// listOllamaTags is a fallback for older Ollama versions that do not expose
// the OpenAI-compatible /v1/models endpoint.
func (c *Client) listOllamaTags(ctx context.Context) ([]ModelInfo, error) {
	url := c.BaseURL + "/api/tags"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &ClientError{Cause: err}
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &ClientError{Cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, &ClientError{Status: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode ollama model list: %w", err)
	}
	out := make([]ModelInfo, 0, len(payload.Models))
	for _, m := range payload.Models {
		if strings.TrimSpace(m.Name) != "" {
			out = append(out, ModelInfo{ID: m.Name, Loaded: true, Status: "available"})
		}
	}
	return out, nil
}

// ResolveOptions selects a provider and, when needed, discovers a model.
type ResolveOptions struct {
	Provider string
	BaseURL  string
	Model    string
	APIKey   string
	Temp     float64
	HTTP     *http.Client
}

// Resolved is a fully configured provider/model pair.
type Resolved struct {
	Provider string
	BaseURL  string
	Model    string
	APIKey   string
}

// Resolve turns a provider selection into a concrete endpoint. When Provider is
// "auto", reachable local servers are probed in parallel. When Model is empty,
// the provider's model list is used if there is exactly one sensible choice.
func Resolve(ctx context.Context, opts ResolveOptions) (*Resolved, error) {
	provider := normalizeProviderName(opts.Provider)
	if provider == "auto" {
		if strings.TrimSpace(opts.BaseURL) != "" {
			provider = "compatible"
		} else {
			return resolveAuto(ctx, opts)
		}
	}

	p, ok := lookupProvider(provider)
	if !ok {
		return nil, fmt.Errorf("unknown provider %q (known: %s)", opts.Provider, strings.Join(ProviderNames(), ", "))
	}
	base := strings.TrimSpace(opts.BaseURL)
	if base == "" {
		base = p.BaseURL
	}
	if base == "" {
		return nil, fmt.Errorf("provider %q requires --base-url", provider)
	}
	apiKey := resolveProviderAPIKey(p, opts.APIKey)
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		var err error
		model, err = discoverModel(ctx, provider, base, apiKey, opts.Temp, opts.HTTP)
		if err != nil {
			return nil, err
		}
	}
	return &Resolved{Provider: provider, BaseURL: base, Model: model, APIKey: apiKey}, nil
}

func resolveAuto(ctx context.Context, opts ResolveOptions) (*Resolved, error) {
	results := ProbeLocal(ctx, opts.Temp, opts.HTTP)
	var reachable []ProbeResult
	for _, r := range results {
		if r.Err == nil && len(r.Models) > 0 {
			reachable = append(reachable, r)
		}
	}
	if len(reachable) == 0 {
		return nil, fmt.Errorf("no local model server found. Start one of: llama-prism (:9931), LM Studio (:1234), Ollama (:11434), or configure a provider with --provider/--base-url/--model")
	}

	model := strings.TrimSpace(opts.Model)
	if model != "" {
		for _, r := range reachable {
			if hasModel(r.Models, model) {
				return resolvedFromProbe(r, model), nil
			}
		}
		return nil, fmt.Errorf("model %q was not found on any reachable provider. Available models:\n%s",
			model, formatProbeModels(reachable))
	}

	// Prefer a provider with exactly one loaded model, then one with exactly
	// one available model.
	for _, r := range reachable {
		if candidate, ok := uniqueModel(r.Models, true); ok {
			return resolvedFromProbe(r, candidate), nil
		}
	}
	for _, r := range reachable {
		if candidate, ok := uniqueModel(r.Models, false); ok {
			return resolvedFromProbe(r, candidate), nil
		}
	}
	return nil, fmt.Errorf("multiple models are available and no default is safe to choose. Set --model. Available models:\n%s",
		formatProbeModels(reachable))
}

func resolvedFromProbe(r ProbeResult, model string) *Resolved {
	return &Resolved{
		Provider: r.Provider,
		BaseURL:  r.BaseURL,
		Model:    model,
		APIKey:   r.APIKey,
	}
}

func discoverModel(ctx context.Context, provider, base, apiKey string, temp float64, httpClient *http.Client) (string, error) {
	client, err := NewClientWithOptions(ClientOptions{
		Provider: provider, BaseURL: base, APIKey: apiKey, Temp: temp, HTTPClient: httpClient,
	})
	if err != nil {
		return "", err
	}
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	models, err := client.ListModels(listCtx)
	if err != nil {
		return "", fmt.Errorf("model is not set and the %s model list could not be read: %v. Set --model explicitly", provider, err)
	}
	if len(models) == 0 {
		return "", fmt.Errorf("model is not set and provider %s returned no models. Set --model explicitly", provider)
	}
	if m, ok := uniqueModel(models, true); ok {
		return m, nil
	}
	if m, ok := uniqueModel(models, false); ok {
		return m, nil
	}
	return "", fmt.Errorf("model is not set for provider %s and multiple models are available. Set --model. Available: %s",
		provider, formatModels(models))
}

func uniqueModel(models []ModelInfo, loadedOnly bool) (string, bool) {
	var ids []string
	for _, m := range models {
		if loadedOnly && !m.Loaded {
			continue
		}
		ids = append(ids, m.ID)
	}
	if len(ids) == 1 {
		return ids[0], true
	}
	return "", false
}

func hasModel(models []ModelInfo, model string) bool {
	for _, m := range models {
		if strings.EqualFold(m.ID, model) {
			return true
		}
	}
	return false
}

func formatModels(models []ModelInfo) string {
	ids := make([]string, 0, len(models))
	for _, m := range models {
		s := m.ID
		if m.Status != "" {
			s += " (" + m.Status + ")"
		}
		ids = append(ids, s)
	}
	return strings.Join(ids, ", ")
}

func formatProbeModels(results []ProbeResult) string {
	var b strings.Builder
	for _, r := range results {
		if r.Err != nil {
			continue
		}
		fmt.Fprintf(&b, "  %s (%s): %s\n", r.Provider, r.BaseURL, formatModels(r.Models))
	}
	return strings.TrimRight(b.String(), "\n")
}

// ProbeResult is one local provider reachability check.
type ProbeResult struct {
	Provider string
	BaseURL  string
	APIKey   string
	Models   []ModelInfo
	Err      error
}

// ProbeLocal checks all local providers in parallel with a short timeout.
func ProbeLocal(ctx context.Context, temp float64, httpClient *http.Client) []ProbeResult {
	names := LocalProviderNames()
	results := make([]ProbeResult, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		p, _ := lookupProvider(name)
		results[i] = ProbeResult{Provider: name, BaseURL: p.BaseURL, APIKey: resolveProviderAPIKey(p, "")}
		wg.Add(1)
		go func(i int, p Provider) {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			defer cancel()
			client, err := NewClientWithOptions(ClientOptions{
				Provider: p.Name, BaseURL: p.BaseURL, APIKey: resolveProviderAPIKey(p, ""),
				Temp: temp, HTTPClient: httpClient,
			})
			if err != nil {
				results[i].Err = err
				return
			}
			models, err := client.ListModels(probeCtx)
			results[i].Err = err
			results[i].Models = models
		}(i, p)
	}
	wg.Wait()
	return results
}
