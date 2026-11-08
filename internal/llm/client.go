// Package llm is a minimal client for OpenAI-compatible chat servers. It
// supports streaming (SSE) chat and tool calls, provider presets, and model
// discovery for llama-prism, LM Studio, Ollama, and hosted APIs.
package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall is a function call requested by the model; Function.Arguments is
// the raw argument string the agent executes.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolFunctionWire `json:"function"`
}

type ToolFunctionWire struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Completion is the aggregated result of one model turn.
type Completion struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
}

type Client struct {
	Provider   string
	BaseURL    string
	Model      string
	Temp       float64
	APIKey     string
	ChatURL    string
	ModelsURL  string
	httpClient *http.Client
}

// ClientOptions configures a Client for one provider.
type ClientOptions struct {
	Provider   string
	BaseURL    string
	Model      string
	APIKey     string
	Temp       float64
	HTTPClient *http.Client
}

// HTTP tuning. There is deliberately no total http.Client.Timeout: local
// models can stream for a long time, and a whole-request deadline would cut a
// healthy generation. Instead the transport bounds connection setup, and the
// response body is wrapped with an idle watchdog that aborts only when no
// bytes arrive for defaultIdleTimeout.
const (
	defaultDialTimeout           = 30 * time.Second
	defaultTLSHandshakeTimeout   = 15 * time.Second
	defaultResponseHeaderTimeout = 10 * time.Minute
	defaultIdleTimeout           = 5 * time.Minute
)

// errIdleTimeout marks a stalled stream so callers can report it clearly
// instead of surfacing context.Canceled.
var errIdleTimeout = errors.New("stream stalled: no data received")

// streamIdleTimeout is a variable so tests can shrink it.
var streamIdleTimeout = defaultIdleTimeout

func newHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			DialContext:           (&net.Dialer{Timeout: defaultDialTimeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
			ResponseHeaderTimeout: defaultResponseHeaderTimeout,
			ExpectContinueTimeout: time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}

// idleTimeoutReader cancels its request when no bytes arrive for d. Each
// successful read resets the timer, so slow-but-steady streams are unaffected.
type idleTimeoutReader struct {
	r     io.Reader
	timer *time.Timer
	d     time.Duration
}

func newIdleTimeoutReader(r io.Reader, cancel context.CancelCauseFunc, d time.Duration) *idleTimeoutReader {
	it := &idleTimeoutReader{r: r, d: d}
	it.timer = time.AfterFunc(d, func() { cancel(errIdleTimeout) })
	return it
}

func (it *idleTimeoutReader) Read(p []byte) (int, error) {
	n, err := it.r.Read(p)
	if n > 0 {
		it.timer.Reset(it.d)
	}
	return n, err
}

func (it *idleTimeoutReader) Close() error {
	it.timer.Stop()
	if c, ok := it.r.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// NewClient builds a Client for the given OpenAI-compatible base URL.
// It is kept for backward compatibility; new code should prefer
// NewClientWithOptions.
func NewClient(baseURL, model string, temp float64) *Client {
	c, err := NewClientWithOptions(ClientOptions{
		Provider: "compatible",
		BaseURL:  baseURL,
		Model:    model,
		Temp:     temp,
	})
	if err != nil {
		// Keep the old constructor total: an invalid URL will surface on
		// the first request with a clear error.
		return &Client{
			Provider:   "compatible",
			BaseURL:    strings.TrimRight(baseURL, "/"),
			Model:      model,
			Temp:       temp,
			ChatURL:    strings.TrimRight(baseURL, "/") + "/v1/chat/completions",
			ModelsURL:  strings.TrimRight(baseURL, "/") + "/v1/models",
			httpClient: newHTTPClient(),
		}
	}
	return c
}

// NewClientWithOptions builds a provider-aware client.
func NewClientWithOptions(opts ClientOptions) (*Client, error) {
	provider := normalizeProviderName(opts.Provider)
	base := strings.TrimSpace(opts.BaseURL)
	if base == "" {
		if p, ok := lookupProvider(provider); ok {
			base = p.BaseURL
		}
	}
	if base == "" {
		return nil, fmt.Errorf("no base URL configured for provider %q", provider)
	}
	chatURL, modelsURL, displayBase := endpointURLs(base)
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = newHTTPClient()
	}
	apiKey := opts.APIKey
	if apiKey == "" {
		if p, ok := lookupProvider(provider); ok {
			apiKey = resolveProviderAPIKey(p, "")
		}
	}
	return &Client{
		Provider:   provider,
		BaseURL:    displayBase,
		Model:      opts.Model,
		Temp:       opts.Temp,
		APIKey:     apiKey,
		ChatURL:    chatURL,
		ModelsURL:  modelsURL,
		httpClient: httpClient,
	}, nil
}

// ClientError describes a failure talking to the model server.
type ClientError struct {
	Status         int
	Body           string
	ModelNotLoaded bool
	Cause          error
}

func (e *ClientError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("LLM server unreachable: %v", e.Cause)
	}
	if e.ModelNotLoaded {
		return fmt.Sprintf("model not loaded: %s", e.Body)
	}
	if e.Status > 0 {
		return fmt.Sprintf("model error (HTTP %d): %s", e.Status, e.Body)
	}
	return fmt.Sprintf("model error: %s", e.Body)
}

func (e *ClientError) Unwrap() error { return e.Cause }

// IsModelNotLoaded reports whether err is a model-not-loaded condition.
func IsModelNotLoaded(err error) bool {
	var e *ClientError
	return errors.As(err, &e) && e.ModelNotLoaded
}

// AsClientError returns the underlying ClientError, if any.
func AsClientError(err error) (*ClientError, bool) {
	var e *ClientError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

type completeRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools"`
	Stream      bool      `json:"stream"`
	Temperature float64   `json:"temperature"`
}

// Complete performs one chat completion. Assistant text deltas are passed to
// onToken (nil to suppress) as they arrive; the aggregated turn is returned.
func (c *Client) Complete(ctx context.Context, messages []Message, tools []Tool, onToken func(string)) (*Completion, error) {
	body, err := json.Marshal(completeRequest{
		Model:       c.Model,
		Messages:    messages,
		Tools:       tools,
		Stream:      true,
		Temperature: c.Temp,
	})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	reqCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.ChatURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, &ClientError{Cause: err}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, &ClientError{Cause: err}
	}
	bodyReader := newIdleTimeoutReader(resp.Body, cancel, streamIdleTimeout)
	defer bodyReader.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.statusError(resp)
	}

	// Some OpenAI-compatible servers ignore stream=true and reply with a
	// single JSON object. Handle that as a fallback instead of failing to
	// parse it as SSE.
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "application/json") {
		comp, err := readJSONCompletion(bodyReader)
		if err != nil {
			return nil, idleError(reqCtx, err)
		}
		return comp, nil
	}

	sc := bufio.NewScanner(bodyReader)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	comp, err := readStream(sc, reqCtx, onToken)
	if err != nil {
		return nil, idleError(reqCtx, err)
	}
	return comp, nil
}

// idleError turns a stalled-stream cause into an actionable error while
// leaving genuine cancellations untouched.
func idleError(ctx context.Context, err error) error {
	if errors.Is(context.Cause(ctx), errIdleTimeout) {
		return &ClientError{Cause: fmt.Errorf("%w for %s", errIdleTimeout, streamIdleTimeout)}
	}
	return err
}

func (c *Client) statusError(resp *http.Response) error {
	limit := io.LimitReader(resp.Body, 8192)
	b, _ := io.ReadAll(limit)
	msg := strings.TrimSpace(string(b))
	if isModelNotLoaded(msg) {
		return &ClientError{
			Status:         resp.StatusCode,
			ModelNotLoaded: true,
			Body:           fmt.Sprintf("model %q is not loaded: %s", c.Model, msg),
		}
	}
	return &ClientError{Status: resp.StatusCode, Body: msg}
}

// isModelNotLoaded reports whether the server body says the model is missing.
func isModelNotLoaded(body string) bool {
	l := strings.ToLower(body)
	return strings.Contains(l, "not loaded") ||
		(strings.Contains(l, "not found") && strings.Contains(l, "model"))
}

// ---- SSE wire types -------------------------------------------------------

type sseChunk struct {
	Choices []sseChoice `json:"choices"`
}

type sseChoice struct {
	Delta        sseDelta `json:"delta"`
	Message      sseDelta `json:"message"` // non-streaming fallback shape
	FinishReason string   `json:"finish_reason"`
}

type sseDelta struct {
	Role      string          `json:"role"`
	Content   *string         `json:"content"`
	ToolCalls []toolCallDelta `json:"tool_calls"`
}

type toolCallDelta struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// readStream parses the SSE stream and aggregates the response.
func readStream(sc *bufio.Scanner, ctx context.Context, onToken func(string)) (*Completion, error) {
	comp := &Completion{}
	var tcs []*toolCallState
	var pending strings.Builder

	process := func(payload string) error {
		payload = strings.TrimSpace(payload)
		if payload == "" || payload == "[DONE]" {
			return nil
		}
		var chunk sseChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("malformed SSE data (JSON decode error): %v", err)
		}
		for _, ch := range chunk.Choices {
			if ch.FinishReason != "" {
				comp.FinishReason = ch.FinishReason
			}
			d := ch.Delta
			if d.Content != nil && *d.Content != "" {
				comp.Content += *d.Content
				if onToken != nil {
					onToken(*d.Content)
				}
			}
			for _, tc := range d.ToolCalls {
				idx := -1
				switch {
				case tc.Index != nil:
					idx = *tc.Index
				case tc.ID != "":
					for i, st := range tcs {
						if st.id == tc.ID {
							idx = i
							break
						}
					}
					if idx < 0 {
						idx = len(tcs)
					}
				case tc.Function.Name != "":
					idx = len(tcs)
				case len(tcs) > 0:
					// Continuation chunk without an index: append to the
					// most recent call.
					idx = len(tcs) - 1
				}
				if idx < 0 {
					idx = 0
				}
				for len(tcs) <= idx {
					tcs = append(tcs, &toolCallState{})
				}
				st := tcs[idx]
				if tc.ID != "" {
					st.id = tc.ID
				}
				if tc.Function.Name != "" {
					st.name = tc.Function.Name
				}
				st.args += tc.Function.Arguments
			}
		}
		return nil
	}

	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			if pending.Len() > 0 {
				if err := process(pending.String()); err != nil {
					return nil, err
				}
				pending.Reset()
			}
			continue
		}
		if strings.HasPrefix(line, ":") { // SSE comment / keep-alive
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		// Fast path: one complete JSON object per data line (the norm for
		// OpenAI-compatible servers). Otherwise buffer multi-line events
		// according to the SSE spec.
		if json.Valid([]byte(payload)) {
			if err := process(payload); err != nil {
				return nil, err
			}
			pending.Reset()
			continue
		}
		if pending.Len() > 0 {
			pending.WriteByte('\n')
		}
		pending.WriteString(payload)
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &ClientError{Status: -1, Body: fmt.Sprintf("stream read error: %v", err)}
	}
	if pending.Len() > 0 {
		if err := process(pending.String()); err != nil {
			return nil, err
		}
	}

	var calls []ToolCall
	for i, t := range tcs {
		if t == nil || t.name == "" {
			continue
		}
		args := strings.TrimSpace(t.args)
		if args == "" {
			args = "{}"
		}
		if !json.Valid([]byte(args)) {
			return nil, fmt.Errorf("invalid tool call arguments for %s (JSON decode error): %s", t.name, args)
		}
		id := t.id
		if id == "" {
			// Some servers omit the id in streamed deltas; invent a stable
			// one so the tool result message can reference it.
			id = fmt.Sprintf("call_%d", i)
		}
		calls = append(calls, ToolCall{
			ID:       id,
			Type:     "function",
			Function: ToolFunctionWire{Name: t.name, Arguments: args},
		})
	}
	comp.ToolCalls = calls
	return comp, nil
}

// readJSONCompletion handles servers that return a normal JSON completion
// even though streaming was requested.
func readJSONCompletion(r io.Reader) (*Completion, error) {
	var resp struct {
		Choices []sseChoice `json:"choices"`
	}
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode completion: %w", err)
	}
	comp := &Completion{}
	for _, ch := range resp.Choices {
		comp.FinishReason = ch.FinishReason
		d := ch.Message
		if d.Content != nil {
			comp.Content += *d.Content
		}
		for _, tc := range d.ToolCalls {
			args := strings.TrimSpace(tc.Function.Arguments)
			if args == "" {
				args = "{}"
			}
			comp.ToolCalls = append(comp.ToolCalls, ToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: ToolFunctionWire{Name: tc.Function.Name, Arguments: args},
			})
		}
	}
	return comp, nil
}

type toolCallState struct {
	id   string
	name string
	args string
}
