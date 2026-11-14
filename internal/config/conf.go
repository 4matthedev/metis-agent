package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"metis-agent/internal/mcp"
)

// parseConf decodes the human-friendly .conf format:
//
//	# comments
//	provider = "ollama"
//	model = gemma4:e4b
//
//	[mcp.github]
//	type = "stdio"
//	command = "npx"
//	args = "-y", "@modelcontextprotocol/server-github"
//
// Keys are case-insensitive and accept dashes or spaces in place of
// underscores. Values may be quoted with " or '. Lists are comma-separated
// (or JSON arrays), and env/header maps may use KEY=value pairs or JSON
// objects. Unknown keys and malformed values fail with a line number so typos
// do not silently fall back to defaults.
func parseConf(path string, data []byte) (File, error) {
	var f File
	text := strings.TrimPrefix(string(data), "\ufeff")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	var server *mcp.ServerConfig
	section := ""
	flush := func() {
		if server != nil {
			f.McpServers = append(f.McpServers, *server)
			server = nil
		}
	}

	for i, raw := range lines {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") {
			flush()
			name, err := parseConfSection(line)
			if err != nil {
				return f, confError(path, lineNo, err)
			}
			lower := strings.ToLower(name)
			if lower == "mcp" || lower == "mcp_servers" {
				section = "mcp"
				server = &mcp.ServerConfig{}
				continue
			}
			dot := strings.IndexByte(name, '.')
			if dot < 0 {
				return f, confError(path, lineNo, fmt.Errorf("unknown section %q", name))
			}
			prefix := strings.ToLower(strings.TrimSpace(name[:dot]))
			if prefix != "mcp" && prefix != "mcp_servers" {
				return f, confError(path, lineNo, fmt.Errorf("unknown section %q", name))
			}
			serverName := strings.TrimSpace(name[dot+1:])
			if serverName == "" {
				return f, confError(path, lineNo, fmt.Errorf("MCP section needs a name"))
			}
			serverName, err = unquoteConfScalar(serverName)
			if err != nil {
				return f, confError(path, lineNo, err)
			}
			section = "mcp"
			server = &mcp.ServerConfig{Name: serverName}
			continue
		}

		key, rawValue, err := splitConfLine(line)
		if err != nil {
			return f, confError(path, lineNo, err)
		}
		rawValue = strings.TrimSpace(stripConfComment(rawValue))
		if section == "mcp" {
			if err := applyConfMCP(server, strings.TrimSpace(key), rawValue); err != nil {
				return f, confError(path, lineNo, err)
			}
			continue
		}
		if err := applyConfValue(&f, normalizeConfKey(key), rawValue); err != nil {
			return f, confError(path, lineNo, err)
		}
	}
	flush()
	return f, nil
}

func confError(path string, line int, err error) error {
	return fmt.Errorf("%s:%d: %w", path, line, err)
}

// parseConfSection parses "[mcp.name]" and permits a trailing # comment.
func parseConfSection(line string) (string, error) {
	end := strings.IndexByte(line, ']')
	if end < 0 {
		return "", fmt.Errorf("missing closing ]")
	}
	rest := strings.TrimSpace(line[end+1:])
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return "", fmt.Errorf("unexpected text after section: %q", rest)
	}
	name := strings.TrimSpace(line[1:end])
	if name == "" {
		return "", fmt.Errorf("empty section name")
	}
	return name, nil
}

// splitConfLine finds the first key/value separator outside quotes. Both
// "key = value" and "key: value" are accepted.
func splitConfLine(line string) (string, string, error) {
	sep := -1
	var quote byte
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && quote != 0 {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '=' || c == ':' {
			sep = i
			break
		}
	}
	if quote != 0 {
		return "", "", fmt.Errorf("unterminated quote")
	}
	if sep < 0 {
		return "", "", fmt.Errorf("expected key = value")
	}
	key := strings.TrimSpace(line[:sep])
	if key == "" {
		return "", "", fmt.Errorf("empty key")
	}
	return key, strings.TrimSpace(line[sep+1:]), nil
}

// stripConfComment removes a trailing # comment. A # only starts a comment at
// the beginning of the value or after whitespace, so URL fragments survive.
func stripConfComment(s string) string {
	var quote byte
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && quote != 0 {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			return s[:i]
		}
	}
	return s
}

func normalizeConfKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, " ", "_")
	return key
}

func setConfValue(dst *string, raw string) error {
	s, err := unquoteConfScalar(raw)
	if err != nil {
		return err
	}
	*dst = s
	return nil
}

func setConfString(dst **string, raw string) error {
	s, err := unquoteConfScalar(raw)
	if err != nil {
		return err
	}
	*dst = &s
	return nil
}

func applyConfValue(f *File, key, raw string) error {
	switch key {
	case "provider":
		return setConfString(&f.Provider, raw)
	case "base_url":
		return setConfString(&f.BaseURL, raw)
	case "model":
		return setConfString(&f.Model, raw)
	case "api_key":
		return setConfString(&f.APIKey, raw)
	case "thinking":
		return setConfString(&f.Thinking, raw)
	case "verify":
		return setConfString(&f.Verify, raw)
	case "embedding_model":
		return setConfString(&f.EmbeddingModel, raw)
	case "highlight":
		return setConfString(&f.Highlight, raw)
	case "temperature":
		v, err := parseConfFloat(raw)
		if err != nil {
			return fmt.Errorf("temperature: %w", err)
		}
		f.Temperature = &v
	case "max_steps":
		v, err := parseConfInt(raw)
		if err != nil {
			return fmt.Errorf("max_steps: %w", err)
		}
		f.MaxSteps = &v
	case "window":
		v, err := parseConfInt(raw)
		if err != nil {
			return fmt.Errorf("window: %w", err)
		}
		f.Window = &v
	case "shell_timeout_sec":
		v, err := parseConfInt(raw)
		if err != nil {
			return fmt.Errorf("shell_timeout_sec: %w", err)
		}
		f.ShellTimeoutSec = &v
	case "verify_timeout_sec":
		v, err := parseConfInt(raw)
		if err != nil {
			return fmt.Errorf("verify_timeout_sec: %w", err)
		}
		f.VerifyTimeoutSec = &v
	case "shell":
		v, err := parseConfBool(raw)
		if err != nil {
			return fmt.Errorf("shell: %w", err)
		}
		f.Shell = &v
	case "session":
		v, err := parseConfBool(raw)
		if err != nil {
			return fmt.Errorf("session: %w", err)
		}
		f.Session = &v
	case "auto_approve":
		v, err := parseConfBool(raw)
		if err != nil {
			return fmt.Errorf("auto_approve: %w", err)
		}
		f.AutoApprove = &v
	case "redact_secrets":
		v, err := parseConfBool(raw)
		if err != nil {
			return fmt.Errorf("redact_secrets: %w", err)
		}
		f.RedactSecrets = &v
	case "instructions":
		items, err := parseConfList(raw)
		if err != nil {
			return fmt.Errorf("instructions: %w", err)
		}
		f.Instructions = append(f.Instructions, items...)
	case "fallback_models":
		items, err := parseConfList(raw)
		if err != nil {
			return fmt.Errorf("fallback_models: %w", err)
		}
		f.FallbackModels = append(f.FallbackModels, items...)
	case "mcp_servers":
		items, err := parseConfServers(raw)
		if err != nil {
			return fmt.Errorf("mcp_servers: %w", err)
		}
		f.McpServers = append(f.McpServers, items...)
	default:
		return fmt.Errorf("unknown key %q", key)
	}
	return nil
}

func parseConfFloat(raw string) (float64, error) {
	s, err := unquoteConfScalar(raw)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("expected a number, got %q", raw)
	}
	return v, nil
}

func parseConfInt(raw string) (int, error) {
	s, err := unquoteConfScalar(raw)
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("expected an integer, got %q", raw)
	}
	return v, nil
}

func parseConfBool(raw string) (bool, error) {
	s, err := unquoteConfScalar(raw)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "on", "1":
		return true, nil
	case "false", "no", "off", "0":
		return false, nil
	default:
		return false, fmt.Errorf("expected true or false, got %q", raw)
	}
}

// unquoteConfScalar decodes a single quoted value. Unquoted values are returned
// as-is after trimming.
func unquoteConfScalar(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if s[0] != '"' && s[0] != '\'' {
		return s, nil
	}
	q := s[0]
	if len(s) < 2 || s[len(s)-1] != q {
		return "", fmt.Errorf("unterminated quoted value")
	}
	body := s[1 : len(s)-1]
	if q == '"' {
		out, err := strconv.Unquote(`"` + body + `"`)
		if err != nil {
			return "", fmt.Errorf("invalid quoted value: %w", err)
		}
		return out, nil
	}

	var b strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' && i+1 < len(body) {
			i++
			switch body[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case '\\':
				b.WriteByte('\\')
			case '\'':
				b.WriteByte('\'')
			default:
				b.WriteByte('\\')
				b.WriteByte(body[i])
			}
			continue
		}
		b.WriteByte(body[i])
	}
	return b.String(), nil
}

// parseConfList accepts either a JSON string array or a comma-separated list.
func parseConfList(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if raw[0] == '[' {
		var out []string
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			return nil, fmt.Errorf("invalid JSON list: %w", err)
		}
		return cleanList(out), nil
	}
	parts, err := splitConfParts(raw, ",")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := unquoteConfScalar(p)
		if err != nil {
			return nil, err
		}
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out, nil
}

// splitConfParts splits on any byte in seps that appears outside quotes. The
// original quoting and escapes are preserved for later unquoting.
func splitConfParts(raw, seps string) ([]string, error) {
	var parts []string
	var b strings.Builder
	var quote byte
	escaped := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if escaped {
			b.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' && quote != 0 {
			escaped = true
			b.WriteByte(c)
			continue
		}
		if quote != 0 {
			b.WriteByte(c)
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			b.WriteByte(c)
			continue
		}
		if strings.IndexByte(seps, c) >= 0 {
			parts = append(parts, b.String())
			b.Reset()
			continue
		}
		b.WriteByte(c)
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	parts = append(parts, b.String())
	return parts, nil
}

// parseConfMap accepts a JSON object of strings or KEY=value pairs separated by
// commas/semicolons.
func parseConfMap(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if raw[0] == '{' {
		var m map[string]string
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, fmt.Errorf("invalid JSON object: %w", err)
		}
		return m, nil
	}
	parts, err := splitConfParts(raw, ",;")
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		k, v, err := splitConfLine(p)
		if err != nil {
			return nil, fmt.Errorf("expected KEY=VALUE: %w", err)
		}
		k, err = unquoteConfScalar(k)
		if err != nil {
			return nil, err
		}
		v, err = unquoteConfScalar(v)
		if err != nil {
			return nil, err
		}
		k = strings.TrimSpace(k)
		if k == "" {
			return nil, fmt.Errorf("empty key")
		}
		out[k] = v
	}
	return out, nil
}

func parseConfServers(raw string) ([]mcp.ServerConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if raw[0] != '[' {
		return nil, fmt.Errorf("expected a JSON array of MCP servers")
	}
	var servers []mcp.ServerConfig
	if err := json.Unmarshal([]byte(raw), &servers); err != nil {
		return nil, fmt.Errorf("invalid MCP server list: %w", err)
	}
	return servers, nil
}

func applyConfMCP(cfg *mcp.ServerConfig, key, raw string) error {
	if cfg == nil {
		return fmt.Errorf("internal: no MCP section")
	}
	if dot := strings.IndexByte(key, '.'); dot >= 0 {
		prefix := strings.ToLower(strings.TrimSpace(key[:dot]))
		name := strings.TrimSpace(key[dot+1:])
		if name == "" {
			return fmt.Errorf("empty %s key", prefix)
		}
		switch prefix {
		case "env":
			v, err := unquoteConfScalar(raw)
			if err != nil {
				return err
			}
			if cfg.Env == nil {
				cfg.Env = map[string]string{}
			}
			cfg.Env[name] = v
			return nil
		case "headers":
			v, err := unquoteConfScalar(raw)
			if err != nil {
				return err
			}
			if cfg.Headers == nil {
				cfg.Headers = map[string]string{}
			}
			cfg.Headers[name] = v
			return nil
		}
	}

	switch normalizeConfKey(key) {
	case "name":
		if err := setConfValue(&cfg.Name, raw); err != nil {
			return err
		}
	case "type":
		if err := setConfValue(&cfg.Type, raw); err != nil {
			return err
		}
	case "transport":
		if err := setConfValue(&cfg.Transport, raw); err != nil {
			return err
		}
	case "command":
		if err := setConfValue(&cfg.Command, raw); err != nil {
			return err
		}
	case "url":
		if err := setConfValue(&cfg.URL, raw); err != nil {
			return err
		}
	case "auth_type":
		if err := setConfValue(&cfg.AuthType, raw); err != nil {
			return err
		}
	case "auth_token":
		if err := setConfValue(&cfg.AuthToken, raw); err != nil {
			return err
		}
	case "args":
		items, err := parseConfList(raw)
		if err != nil {
			return fmt.Errorf("args: %w", err)
		}
		cfg.Args = items
	case "env":
		m, err := parseConfMap(raw)
		if err != nil {
			return fmt.Errorf("env: %w", err)
		}
		if len(m) > 0 && cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		for k, v := range m {
			cfg.Env[k] = v
		}
	case "headers":
		m, err := parseConfMap(raw)
		if err != nil {
			return fmt.Errorf("headers: %w", err)
		}
		if len(m) > 0 && cfg.Headers == nil {
			cfg.Headers = map[string]string{}
		}
		for k, v := range m {
			cfg.Headers[k] = v
		}
	default:
		return fmt.Errorf("unknown key %q in MCP section", key)
	}
	return nil
}

// looksLikeJSON chooses the parser. Extensions are authoritative; for
// extensionless paths a leading { is treated as JSON.
func looksLikeJSON(path string, data []byte) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return true
	case ".conf", ".cfg", ".ini", ".config":
		return false
	}
	data = bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\ufeff")))
	return len(data) > 0 && data[0] == '{'
}

func isConfPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".conf", ".cfg", ".ini", ".config":
		return true
	}
	return false
}

// renderDefaultConf writes a commented starter file. The output round-trips
// through parseConf, so --init-config doubles as documentation.
func renderDefaultConf(v Values) []byte {
	var b strings.Builder
	line := func(format string, args ...any) {
		fmt.Fprintf(&b, format+"\n", args...)
	}

	line("# Metis Agent configuration")
	line("#")
	line("# Settings use simple key = value syntax. Lines starting with # are comments.")
	line("# Config files are overlaid in this order, later wins:")
	line("#   global config.json (legacy) < global config.conf <")
	line("#   project .metis-agent.json (legacy) < project .metis-agent.conf <")
	line("#   METIS_* environment variables < command-line flags")
	line("")
	line("# Provider: auto, llama-prism, lmstudio, ollama, openai, openrouter, groq,")
	line("# together, or compatible.")
	line("provider = %s", strconv.Quote(v.Provider))
	line("# Base URL for a compatible endpoint. Leave empty to auto-detect local servers.")
	line("base_url = %s", strconv.Quote(v.BaseURL))
	line("# Model name. Leave empty to use the first loaded model.")
	line("model = %s", strconv.Quote(v.Model))
	line("# API key for hosted providers. Local servers do not need one.")
	line("# api_key = \"\"")
	line("")
	line("# Sampling temperature (0.0 - 2.0).")
	line("temperature = %s", strconv.FormatFloat(v.Temperature, 'f', -1, 64))
	line("# Reasoning effort: none, low, medium, high, xhigh.")
	line("thinking = %s", strconv.Quote(v.Thinking))
	line("# Maximum model/tool steps per task.")
	line("max_steps = %d", v.MaxSteps)
	line("# Conversation messages kept in context.")
	line("window = %d", v.Window)
	line("")
	line("# Allow the shell tool.")
	line("shell = %t", v.Shell)
	line("shell_timeout_sec = %d", v.ShellTimeoutSec)
	line("# Persist conversations to disk.")
	line("session = %t", v.Session)
	line("# Syntax highlighting: auto, dark, light, off.")
	line("highlight = %s", strconv.Quote(v.Highlight))
	line("# Run dangerous tools without asking. Keep false unless you trust the workspace.")
	line("auto_approve = %t", v.AutoApprove)
	line("# Mask credentials in tool output.")
	line("redact_secrets = %t", v.RedactSecrets)
	line("")
	line("# Command to run automatically after a turn changes files (empty disables).")
	line("verify = %s", strconv.Quote(v.Verify))
	line("verify_timeout_sec = %d", v.VerifyTimeoutSec)
	line("# Embedding model for semantic_search (empty disables the tool).")
	line("embedding_model = %s", strconv.Quote(v.EmbeddingModel))
	line("")
	if len(v.Instructions) > 0 {
		line("instructions = %s", quoteConfList(v.Instructions))
	} else {
		line("# Extra instruction files, comma-separated:")
		line("# instructions = \"AGENTS.md\", \"docs/rules.md\"")
	}
	if len(v.FallbackModels) > 0 {
		line("fallback_models = %s", quoteConfList(v.FallbackModels))
	} else {
		line("# Models to try when the active model fails:")
		line("# fallback_models = \"model-a\", \"model-b\"")
	}
	line("")
	if len(v.McpServers) > 0 {
		for _, s := range v.McpServers {
			writeConfMCPServer(&b, s)
		}
	} else {
		line("# MCP servers (optional). Each section becomes one server:")
		line("#")
		line("# [mcp.github]")
		line("# type = \"stdio\"")
		line("# command = \"npx\"")
		line("# args = \"-y\", \"@modelcontextprotocol/server-github\"")
		line("# env.GITHUB_PERSONAL_ACCESS_TOKEN = \"...\"")
		line("#")
		line("# [mcp.remote]")
		line("# type = \"http\"")
		line("# url = \"https://mcp.example.com/mcp\"")
		line("# auth_type = \"bearer\"")
		line("# auth_token = \"...\"")
	}
	return []byte(b.String())
}

func quoteConfList(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = strconv.Quote(s)
	}
	return strings.Join(parts, ", ")
}

func writeConfMCPServer(b *strings.Builder, s mcp.ServerConfig) {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		name = "server"
	}
	fmt.Fprintf(b, "[mcp.%s]\n", name)
	if s.Type != "" {
		fmt.Fprintf(b, "type = %s\n", strconv.Quote(s.Type))
	}
	if s.Transport != "" {
		fmt.Fprintf(b, "transport = %s\n", strconv.Quote(s.Transport))
	}
	if s.Command != "" {
		fmt.Fprintf(b, "command = %s\n", strconv.Quote(s.Command))
	}
	if len(s.Args) > 0 {
		fmt.Fprintf(b, "args = %s\n", quoteConfList(s.Args))
	}
	if s.URL != "" {
		fmt.Fprintf(b, "url = %s\n", strconv.Quote(s.URL))
	}
	if s.AuthType != "" {
		fmt.Fprintf(b, "auth_type = %s\n", strconv.Quote(s.AuthType))
	}
	if s.AuthToken != "" {
		fmt.Fprintf(b, "auth_token = %s\n", strconv.Quote(s.AuthToken))
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "env.%s = %s\n", k, strconv.Quote(s.Env[k]))
	}
	keys = keys[:0]
	for k := range s.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "headers.%s = %s\n", k, strconv.Quote(s.Headers[k]))
	}
	fmt.Fprintln(b)
}
