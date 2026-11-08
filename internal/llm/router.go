package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Router is a Completer that can switch models at runtime and retry with a
// fallback model when the active one is unavailable.
type Router struct {
	provider  string
	baseURL   string
	apiKey    string
	temp      float64
	fallbacks []string

	mu       sync.RWMutex
	client   *Client
	model    string
	onSwitch func(string)
}

// NewRouter builds a router for one provider and an ordered fallback chain.
func NewRouter(provider, baseURL, apiKey, model string, temp float64, fallbacks []string) (*Router, error) {
	r := &Router{
		provider:  provider,
		baseURL:   baseURL,
		apiKey:    apiKey,
		temp:      temp,
		fallbacks: dedupe(append([]string{model}, fallbacks...)),
		model:     model,
	}
	c, err := r.clientFor(model)
	if err != nil {
		return nil, err
	}
	r.client = c
	return r, nil
}

func (r *Router) clientFor(model string) (*Client, error) {
	return NewClientWithOptions(ClientOptions{
		Provider: r.provider,
		BaseURL:  r.baseURL,
		Model:    model,
		APIKey:   r.apiKey,
		Temp:     r.temp,
	})
}

// Complete tries the active model first, then each fallback in order. Context
// cancellation stops the chain immediately.
func (r *Router) Complete(ctx context.Context, messages []Message, tools []Tool, onToken func(string)) (*Completion, error) {
	r.mu.RLock()
	client, model := r.client, r.model
	r.mu.RUnlock()

	comp, err := client.Complete(ctx, messages, tools, onToken)
	if err == nil {
		return comp, nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	firstErr := err

	r.mu.RLock()
	candidates := append([]string(nil), r.fallbacks...)
	r.mu.RUnlock()
	for _, candidate := range candidates {
		if candidate == "" || candidate == model {
			continue
		}
		next, cerr := r.clientFor(candidate)
		if cerr != nil {
			continue
		}
		comp, err = next.Complete(ctx, messages, tools, onToken)
		if err == nil {
			r.mu.Lock()
			r.client = next
			r.model = candidate
			cb := r.onSwitch
			r.mu.Unlock()
			if cb != nil {
				cb(candidate)
			}
			return comp, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
	}
	if len(candidates) > 1 {
		return nil, fmt.Errorf("all models failed (starting with %q): %w", model, firstErr)
	}
	return nil, firstErr
}

// SetModel switches the active model.
func (r *Router) SetModel(model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return errors.New("model name must not be empty")
	}
	c, err := r.clientFor(model)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.client = c
	r.model = model
	if !contains(r.fallbacks, model) {
		r.fallbacks = append([]string{model}, r.fallbacks...)
	}
	cb := r.onSwitch
	r.mu.Unlock()
	if cb != nil {
		cb(model)
	}
	return nil
}

// Model returns the active model name.
func (r *Router) Model() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.model
}

// Provider returns the provider name.
func (r *Router) Provider() string { return r.provider }

// BaseURL returns the provider base URL.
func (r *Router) BaseURL() string { return r.baseURL }

// SetSwitchCallback installs a function called after an automatic or manual
// model switch.
func (r *Router) SetSwitchCallback(fn func(string)) {
	r.mu.Lock()
	r.onSwitch = fn
	r.mu.Unlock()
}

// ListModels returns the provider's model catalog.
func (r *Router) ListModels(ctx context.Context) ([]ModelInfo, error) {
	r.mu.RLock()
	client := r.client
	r.mu.RUnlock()
	return client.ListModels(ctx)
}

func dedupe(in []string) []string {
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
