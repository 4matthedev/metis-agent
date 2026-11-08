// Package config loads Metis settings from JSON files and the environment.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"metis-agent/internal/mcp"
)

// Values is the fully resolved runtime configuration.
type Values struct {
	Provider         string
	BaseURL          string
	Model            string
	APIKey           string
	Temperature      float64
	MaxSteps         int
	Window           int
	Shell            bool
	ShellTimeoutSec  int
	Session          bool
	Highlight        string // auto, dark, light
	AutoApprove      bool
	RedactSecrets    bool
	Verify           string
	VerifyTimeoutSec int
	Instructions     []string
	FallbackModels   []string
	McpServers       []mcp.ServerConfig
}

// Defaults are safe, provider-neutral defaults. The provider is auto-detected
// and the model is discovered unless explicitly configured.
func Defaults() Values {
	return Values{
		Provider:         "auto",
		Temperature:      0.3,
		MaxSteps:         40,
		Window:           64,
		Shell:            true,
		ShellTimeoutSec:  120,
		Session:          true,
		Highlight:        "auto",
		AutoApprove:      false,
		RedactSecrets:    true,
		VerifyTimeoutSec: 120,
		McpServers:       nil,
	}
}

// File is the on-disk JSON shape. Pointers distinguish "unset" from zero
// values so project config can override global config cleanly.
type File struct {
	Provider         *string            `json:"provider,omitempty"`
	BaseURL          *string            `json:"base_url,omitempty"`
	Model            *string            `json:"model,omitempty"`
	APIKey           *string            `json:"api_key,omitempty"`
	Temperature      *float64           `json:"temperature,omitempty"`
	MaxSteps         *int               `json:"max_steps,omitempty"`
	Window           *int               `json:"window,omitempty"`
	Shell            *bool              `json:"shell,omitempty"`
	ShellTimeoutSec  *int               `json:"shell_timeout_sec,omitempty"`
	Session          *bool              `json:"session,omitempty"`
	Highlight        *string            `json:"highlight,omitempty"`
	AutoApprove      *bool              `json:"auto_approve,omitempty"`
	RedactSecrets    *bool              `json:"redact_secrets,omitempty"`
	Verify           *string            `json:"verify,omitempty"`
	VerifyTimeoutSec *int               `json:"verify_timeout_sec,omitempty"`
	Instructions     []string           `json:"instructions,omitempty"`
	FallbackModels   []string           `json:"fallback_models,omitempty"`
	McpServers       []mcp.ServerConfig `json:"mcp_servers,omitempty"`
}

// Apply overlays non-nil file values onto v.
func (f File) Apply(v *Values) {
	if f.Provider != nil {
		v.Provider = strings.TrimSpace(*f.Provider)
	}
	if f.BaseURL != nil {
		v.BaseURL = strings.TrimSpace(*f.BaseURL)
	}
	if f.Model != nil {
		v.Model = strings.TrimSpace(*f.Model)
	}
	if f.APIKey != nil {
		v.APIKey = strings.TrimSpace(*f.APIKey)
	}
	if f.Temperature != nil {
		v.Temperature = *f.Temperature
	}
	if f.MaxSteps != nil {
		v.MaxSteps = *f.MaxSteps
	}
	if f.Window != nil {
		v.Window = *f.Window
	}
	if f.Shell != nil {
		v.Shell = *f.Shell
	}
	if f.ShellTimeoutSec != nil {
		v.ShellTimeoutSec = *f.ShellTimeoutSec
	}
	if f.Session != nil {
		v.Session = *f.Session
	}
	if f.Highlight != nil {
		v.Highlight = strings.ToLower(strings.TrimSpace(*f.Highlight))
	}
	if f.AutoApprove != nil {
		v.AutoApprove = *f.AutoApprove
	}
	if f.RedactSecrets != nil {
		v.RedactSecrets = *f.RedactSecrets
	}
	if f.Verify != nil {
		v.Verify = strings.TrimSpace(*f.Verify)
	}
	if f.VerifyTimeoutSec != nil {
		v.VerifyTimeoutSec = *f.VerifyTimeoutSec
	}
	if len(f.Instructions) > 0 {
		v.Instructions = cleanList(f.Instructions)
	}
	if len(f.FallbackModels) > 0 {
		v.FallbackModels = cleanList(f.FallbackModels)
	}
	if len(f.McpServers) > 0 {
		v.McpServers = f.McpServers
	}
}

// cleanList trims entries and drops empty ones.
func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// LoadFile reads and decodes a JSON config file.
func LoadFile(path string) (File, error) {
	var f File
	b, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("parse %s: %w", path, err)
	}
	return f, nil
}

// ConfigDir returns the platform config directory for Metis.
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "metis-agent"), nil
}

// GlobalPath returns the global config file path.
func GlobalPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// ProjectPath returns the per-project config path for dir.
func ProjectPath(dir string) string {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	return filepath.Join(dir, ".metis-agent.json")
}

// DataDir returns the platform data directory for sessions.
func DataDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if base == "" {
			var err error
			base, err = os.UserConfigDir()
			if err != nil {
				return "", err
			}
		}
		return filepath.Join(base, "metis-agent"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "metis-agent"), nil
	default:
		base := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, ".local", "share")
		}
		return filepath.Join(base, "metis-agent"), nil
	}
}

// ApplyEnv overlays supported environment variables onto v.
func ApplyEnv(v *Values) error {
	if s := strings.TrimSpace(os.Getenv("METIS_PROVIDER")); s != "" {
		v.Provider = s
	}
	if s := strings.TrimSpace(os.Getenv("METIS_BASE_URL")); s != "" {
		v.BaseURL = s
	}
	if s := strings.TrimSpace(os.Getenv("METIS_MODEL")); s != "" {
		v.Model = s
	}
	if s := strings.TrimSpace(os.Getenv("METIS_API_KEY")); s != "" {
		v.APIKey = s
	}
	if s := strings.TrimSpace(os.Getenv("METIS_TEMPERATURE")); s != "" {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return fmt.Errorf("METIS_TEMPERATURE: %w", err)
		}
		v.Temperature = f
	}
	if s := strings.TrimSpace(os.Getenv("METIS_MAX_STEPS")); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("METIS_MAX_STEPS: %w", err)
		}
		v.MaxSteps = n
	}
	if s := strings.TrimSpace(os.Getenv("METIS_WINDOW")); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("METIS_WINDOW: %w", err)
		}
		v.Window = n
	}
	if s := strings.TrimSpace(os.Getenv("METIS_SHELL")); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("METIS_SHELL: %w", err)
		}
		v.Shell = b
	}
	if s := strings.TrimSpace(os.Getenv("METIS_SHELL_TIMEOUT_SEC")); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("METIS_SHELL_TIMEOUT_SEC: %w", err)
		}
		v.ShellTimeoutSec = n
	}
	if s := strings.TrimSpace(os.Getenv("METIS_SESSION")); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("METIS_SESSION: %w", err)
		}
		v.Session = b
	}
	if s := strings.TrimSpace(os.Getenv("METIS_HIGHLIGHT")); s != "" {
		v.Highlight = strings.ToLower(s)
	}
	if s := strings.TrimSpace(os.Getenv("METIS_AUTO_APPROVE")); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("METIS_AUTO_APPROVE: %w", err)
		}
		v.AutoApprove = b
	}
	if s := strings.TrimSpace(os.Getenv("METIS_REDACT_SECRETS")); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("METIS_REDACT_SECRETS: %w", err)
		}
		v.RedactSecrets = b
	}
	if s := strings.TrimSpace(os.Getenv("METIS_VERIFY")); s != "" {
		v.Verify = s
	}
	if s := strings.TrimSpace(os.Getenv("METIS_VERIFY_TIMEOUT_SEC")); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("METIS_VERIFY_TIMEOUT_SEC: %w", err)
		}
		v.VerifyTimeoutSec = n
	}
	if s := strings.TrimSpace(os.Getenv("METIS_INSTRUCTIONS")); s != "" {
		v.Instructions = splitList(s)
	}
	if s := strings.TrimSpace(os.Getenv("METIS_FALLBACK_MODELS")); s != "" {
		v.FallbackModels = splitList(s)
	}
	if s := strings.TrimSpace(os.Getenv("METIS_MCP_SERVERS")); s != "" {
		var servers []mcp.ServerConfig
		if err := json.Unmarshal([]byte(s), &servers); err != nil {
			return fmt.Errorf("METIS_MCP_SERVERS: %w", err)
		}
		v.McpServers = mergeServers(v.McpServers, servers)
	}
	return nil
}

// Validate checks the resolved values.
func (v Values) Validate() error {
	if strings.TrimSpace(v.Provider) == "" {
		return fmt.Errorf("provider must not be empty")
	}
	if v.Temperature < 0 || v.Temperature > 2 {
		return fmt.Errorf("temperature must be between 0 and 2")
	}
	if v.MaxSteps < 1 {
		return fmt.Errorf("max_steps must be at least 1")
	}
	if v.Window < 1 {
		return fmt.Errorf("window must be at least 1")
	}
	if v.ShellTimeoutSec < 1 {
		return fmt.Errorf("shell_timeout_sec must be at least 1")
	}
	if v.VerifyTimeoutSec < 1 {
		return fmt.Errorf("verify_timeout_sec must be at least 1")
	}
	switch v.Highlight {
	case "auto", "dark", "light", "off":
	default:
		return fmt.Errorf("highlight must be auto, dark, light, or off")
	}
	return nil
}

// WriteDefault writes a starter config file with 0600 permissions.
func WriteDefault(path string, v Values) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f := File{
		Provider:         &v.Provider,
		BaseURL:          &v.BaseURL,
		Model:            &v.Model,
		Temperature:      &v.Temperature,
		MaxSteps:         &v.MaxSteps,
		Window:           &v.Window,
		Shell:            &v.Shell,
		ShellTimeoutSec:  &v.ShellTimeoutSec,
		Session:          &v.Session,
		Highlight:        &v.Highlight,
		AutoApprove:      &v.AutoApprove,
		RedactSecrets:    &v.RedactSecrets,
		Verify:           &v.Verify,
		VerifyTimeoutSec: &v.VerifyTimeoutSec,
		Instructions:     v.Instructions,
		FallbackModels:   v.FallbackModels,
		McpServers:       v.McpServers,
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
}

// splitList splits a comma-separated environment value.
func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// mergeServers merges env-provided servers on top of file servers: a file and
// an env entry with the same name (after defaulting name) override each other,
// with env winning. Empty-name entries are appended as-is.
func mergeServers(base, env []mcp.ServerConfig) []mcp.ServerConfig {
	if len(env) == 0 {
		return base
	}
	out := append([]mcp.ServerConfig(nil), base...)
	for _, e := range env {
		name := e.Name
		if name == "" {
			name = mcp.DefaultServerName(e)
		}
		replaced := false
		for i := range out {
			of := out[i].Name
			if of == "" {
				of = mcp.DefaultServerName(out[i])
			}
			if of == name {
				out[i] = e
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, e)
		}
	}
	return out
}
