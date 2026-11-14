package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseConf(t *testing.T) {
	data := []byte(`
# Metis configuration
provider = "ollama"
base_url = http://127.0.0.1:11434  # local server
model = gemma4:e4b
api_key = 'secret key'
temperature = 0.6
thinking = high
max_steps = 12
window = 20
shell = no
shell_timeout_sec = 30
session = off
highlight = DARK
auto_approve = yes
redact_secrets = 0
verify = "go test ./..."
verify_timeout_sec = 90
instructions = "AGENTS.md", docs/rules.md
fallback_models = model-a, "model b"
embedding_model = nomic-embed-text

[mcp.github]
type = stdio
command = npx
args = "-y", "@modelcontextprotocol/server-github"
env.GITHUB_PERSONAL_ACCESS_TOKEN = "token"

[mcp.remote]
type = http
url = https://mcp.example.com/mcp
auth_type = bearer
auth_token = "abc"
headers.X-Trace = "1"
`)

	f, err := parseConf("test.conf", data)
	if err != nil {
		t.Fatal(err)
	}
	v := Defaults()
	f.Apply(&v)

	if v.Provider != "ollama" || v.BaseURL != "http://127.0.0.1:11434" || v.Model != "gemma4:e4b" {
		t.Fatalf("strings not applied: %+v", v)
	}
	if v.APIKey != "secret key" {
		t.Fatalf("api_key = %q", v.APIKey)
	}
	if v.Temperature != 0.6 || v.MaxSteps != 12 || v.Window != 20 {
		t.Fatalf("numbers not applied: %+v", v)
	}
	if v.Thinking != "high" || v.Verify != "go test ./..." || v.VerifyTimeoutSec != 90 {
		t.Fatalf("thinking/verify not applied: %+v", v)
	}
	if v.Shell || v.ShellTimeoutSec != 30 || v.Session || !v.AutoApprove || v.RedactSecrets {
		t.Fatalf("booleans not applied: %+v", v)
	}
	if v.Highlight != "dark" {
		t.Fatalf("highlight = %q", v.Highlight)
	}
	if !reflect.DeepEqual(v.Instructions, []string{"AGENTS.md", "docs/rules.md"}) {
		t.Fatalf("instructions = %#v", v.Instructions)
	}
	if !reflect.DeepEqual(v.FallbackModels, []string{"model-a", "model b"}) {
		t.Fatalf("fallback_models = %#v", v.FallbackModels)
	}
	if v.EmbeddingModel != "nomic-embed-text" {
		t.Fatalf("embedding_model = %q", v.EmbeddingModel)
	}

	if len(v.McpServers) != 2 {
		t.Fatalf("mcp servers = %d", len(v.McpServers))
	}
	gh := v.McpServers[0]
	if gh.Name != "github" || gh.Type != "stdio" || gh.Command != "npx" {
		t.Fatalf("github server = %+v", gh)
	}
	if !reflect.DeepEqual(gh.Args, []string{"-y", "@modelcontextprotocol/server-github"}) {
		t.Fatalf("github args = %#v", gh.Args)
	}
	if gh.Env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "token" {
		t.Fatalf("github env = %#v", gh.Env)
	}
	remote := v.McpServers[1]
	if remote.Name != "remote" || remote.URL != "https://mcp.example.com/mcp" || remote.AuthToken != "abc" {
		t.Fatalf("remote server = %+v", remote)
	}
	if remote.Headers["X-Trace"] != "1" {
		t.Fatalf("remote headers = %#v", remote.Headers)
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("parsed values should validate: %v", err)
	}
}

func TestParseConfCommentsAndQuotes(t *testing.T) {
	data := []byte(`model = "gem#ma" # a comment
verify = 'echo "hi" # still part of the command'
instructions = "a,b", 'c d'
base_url = https://example.com/#fragment
`)
	f, err := parseConf("quotes.conf", data)
	if err != nil {
		t.Fatal(err)
	}
	v := Defaults()
	f.Apply(&v)

	if v.Model != "gem#ma" {
		t.Fatalf("model = %q", v.Model)
	}
	if v.Verify != `echo "hi" # still part of the command` {
		t.Fatalf("verify = %q", v.Verify)
	}
	if !reflect.DeepEqual(v.Instructions, []string{"a,b", "c d"}) {
		t.Fatalf("instructions = %#v", v.Instructions)
	}
	if v.BaseURL != "https://example.com/#fragment" {
		t.Fatalf("base_url = %q", v.BaseURL)
	}
}

func TestParseConfMCPMapAndJSONList(t *testing.T) {
	data := []byte(`
mcp_servers = [{"name":"inline","type":"stdio","command":"npx"}]

[mcp.mapped]
type = "stdio"
command = "server"
args = ["--one", "--two"]
env = A=1, B="two, three"
headers = X-Test=yes

[mcp.named]
name = "renamed"
type = "http"
url = "https://example.com/mcp"
`)
	f, err := parseConf("mcp.conf", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.McpServers) != 3 {
		t.Fatalf("servers = %d", len(f.McpServers))
	}
	if f.McpServers[0].Name != "inline" {
		t.Fatalf("inline server = %+v", f.McpServers[0])
	}
	mapped := f.McpServers[1]
	if mapped.Name != "mapped" || !reflect.DeepEqual(mapped.Args, []string{"--one", "--two"}) {
		t.Fatalf("mapped server = %+v", mapped)
	}
	if mapped.Env["A"] != "1" || mapped.Env["B"] != "two, three" {
		t.Fatalf("mapped env = %#v", mapped.Env)
	}
	if mapped.Headers["X-Test"] != "yes" {
		t.Fatalf("mapped headers = %#v", mapped.Headers)
	}
	if f.McpServers[2].Name != "renamed" {
		t.Fatalf("name override = %+v", f.McpServers[2])
	}
}

func TestParseConfRejectsBadInput(t *testing.T) {
	cases := []string{
		"unknown = 1\n",
		"temperature = hot\n",
		"shell = maybe\n",
		"max_steps = many\n",
		"missing separator\n",
		"[unknown]\n",
		"[mcp.]\n",
		`model = "unterminated` + "\n",
		"[mcp.x]\nargs = \"unterminated\n",
	}
	for _, input := range cases {
		if _, err := parseConf("bad.conf", []byte(input)); err == nil {
			t.Errorf("parseConf accepted %q", input)
		}
	}
}

func TestLoadFileDetectsFormat(t *testing.T) {
	dir := t.TempDir()

	jsonPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(jsonPath, []byte(`{"model":"json-model"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := LoadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if f.Model == nil || *f.Model != "json-model" {
		t.Fatalf("JSON model = %#v", f.Model)
	}

	confPath := filepath.Join(dir, "config.conf")
	if err := os.WriteFile(confPath, []byte("model = conf-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err = LoadFile(confPath)
	if err != nil {
		t.Fatal(err)
	}
	if f.Model == nil || *f.Model != "conf-model" {
		t.Fatalf(".conf model = %#v", f.Model)
	}

	// Extensionless JSON is sniffed.
	noExt := filepath.Join(dir, "settings")
	if err := os.WriteFile(noExt, []byte("\ufeff  {\"model\": \"sniffed\"}"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err = LoadFile(noExt)
	if err != nil {
		t.Fatal(err)
	}
	if f.Model == nil || *f.Model != "sniffed" {
		t.Fatalf("sniffed model = %#v", f.Model)
	}

	// Extensionless key=value is also accepted.
	confNoExt := filepath.Join(dir, "settings.conf")
	if err := os.WriteFile(confNoExt, []byte("model = raw-conf\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err = LoadFile(confNoExt)
	if err != nil {
		t.Fatal(err)
	}
	if f.Model == nil || *f.Model != "raw-conf" {
		t.Fatalf("conf model = %#v", f.Model)
	}
}

func TestWriteDefaultConfRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.conf")
	if err := WriteDefault(path, Defaults()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`provider = "auto"`)) {
		t.Fatalf("starter does not look like .conf:\n%s", b)
	}
	if !bytes.Contains(b, []byte("[mcp.github]")) {
		t.Fatalf("starter does not document MCP sections:\n%s", b)
	}

	f, err := LoadFile(path)
	if err != nil {
		t.Fatalf("starter does not round-trip: %v\n%s", err, b)
	}
	got := Defaults()
	f.Apply(&got)
	if !reflect.DeepEqual(got, Defaults()) {
		t.Fatalf("round-trip changed defaults:\ngot  %+v\nwant %+v", got, Defaults())
	}
}

func TestWriteDefaultJSONStillJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := WriteDefault(path, Defaults()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		t.Fatalf("JSON starter does not start with {:\n%s", b)
	}
	if _, err := LoadFile(path); err != nil {
		t.Fatalf("JSON starter does not load: %v", err)
	}
}

func TestConfPaths(t *testing.T) {
	if got := ProjectConfPath(""); got != ".metis-agent.conf" {
		t.Fatalf("ProjectConfPath(\"\") = %q", got)
	}
	if got := ProjectConfPath("/tmp/proj"); got != "/tmp/proj/.metis-agent.conf" {
		t.Fatalf("ProjectConfPath = %q", got)
	}

	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	got, err := GlobalConfPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "config.conf" {
		t.Fatalf("GlobalConfPath base = %q", filepath.Base(got))
	}
	if !strings.HasSuffix(filepath.Dir(got), "metis-agent") {
		t.Fatalf("GlobalConfPath dir = %q", filepath.Dir(got))
	}
}
