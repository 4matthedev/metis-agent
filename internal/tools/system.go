package tools

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// NewSystemInfoTool reports basic host and runtime information.
func NewSystemInfoTool(workDir string) *Tool {
	return &Tool{
		Name: "system_info",
		Description: "Report host and runtime information: OS, architecture, CPU count, user, home directory, " +
			"temporary directory, Go version, and the current project path.",
		Parameters: objectSchema(map[string]any{}),
		Handler: func(_ context.Context, _ map[string]any) (string, error) {
			var b strings.Builder
			if host, err := os.Hostname(); err == nil {
				fmt.Fprintf(&b, "hostname: %s\n", host)
			}
			fmt.Fprintf(&b, "os: %s\n", runtime.GOOS)
			fmt.Fprintf(&b, "arch: %s\n", runtime.GOARCH)
			fmt.Fprintf(&b, "cpus: %d\n", runtime.NumCPU())
			fmt.Fprintf(&b, "go: %s\n", runtime.Version())
			if u, err := user.Current(); err == nil {
				fmt.Fprintf(&b, "user: %s\n", u.Username)
				fmt.Fprintf(&b, "home: %s\n", u.HomeDir)
			}
			fmt.Fprintf(&b, "temp: %s\n", os.TempDir())
			if wd, err := os.Getwd(); err == nil {
				fmt.Fprintf(&b, "cwd: %s\n", wd)
			}
			fmt.Fprintf(&b, "project: %s\n", workDir)
			fmt.Fprintf(&b, "time: %s\n", time.Now().Format(time.RFC3339))
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}

// NewEnvGetTool reads environment variables with secret redaction.
func NewEnvGetTool() *Tool {
	return &Tool{
		Name: "env_get",
		Description: "Read environment variables. Values whose names look like secrets (KEY, TOKEN, SECRET, PASSWORD, ...) " +
			"are redacted unless show_secrets=true. Use name or prefix to narrow the result.",
		Parameters: objectSchema(map[string]any{
			"name":         propSchema{Type: "string", Description: "Exact variable name."},
			"prefix":       propSchema{Type: "string", Description: "Only names with this prefix (case-insensitive)."},
			"max_entries":  propSchema{Type: "integer", Description: "Maximum entries (default 200, max 2000)."},
			"show_secrets": propSchema{Type: "boolean", Description: "Show secret-looking values instead of redacting them."},
		}),
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			name := strings.TrimSpace(str(args["name"]))
			prefix := strings.ToUpper(strings.TrimSpace(str(args["prefix"])))
			maxEntries := clamp(intArg(args, "max_entries", 200), 1, 2000)
			showSecrets := boolArg(args, "show_secrets")

			if name != "" {
				v, ok := os.LookupEnv(name)
				if !ok {
					return fmt.Sprintf("%s is not set", name), nil
				}
				if !showSecrets && looksSecret(name) {
					return fmt.Sprintf("%s=<redacted>", name), nil
				}
				return fmt.Sprintf("%s=%s", name, v), nil
			}

			env := os.Environ()
			sort.Strings(env)
			var out []string
			for _, entry := range env {
				key, value, _ := strings.Cut(entry, "=")
				if prefix != "" && !strings.HasPrefix(strings.ToUpper(key), prefix) {
					continue
				}
				if !showSecrets && looksSecret(key) {
					value = "<redacted>"
				}
				out = append(out, key+"="+value)
				if len(out) >= maxEntries {
					break
				}
			}
			if len(out) == 0 {
				return "no matching environment variables", nil
			}
			if len(out) >= maxEntries {
				out = append(out, fmt.Sprintf("... (stopped after %d entries)", maxEntries))
			}
			return strings.Join(out, "\n"), nil
		},
	}
}

func looksSecret(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE", "AUTH"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

// NewWhichTool resolves executables using PATH.
func NewWhichTool() *Tool {
	return &Tool{
		Name:        "which",
		Description: "Resolve one or more executable names using PATH and report their absolute locations.",
		Parameters: objectSchema(map[string]any{
			"command": propSchema{Type: "string", Description: "Single executable name."},
			"commands": map[string]any{
				"type":        "array",
				"description": "Several executable names.",
				"items":       map[string]any{"type": "string"},
				"maxItems":    50,
			},
		}),
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			names := stringList(args["commands"])
			if name := strings.TrimSpace(str(args["command"])); name != "" {
				names = append(names, name)
			}
			if len(names) == 0 {
				return "", fmt.Errorf("command or commands is required")
			}
			if len(names) > 50 {
				return "", fmt.Errorf("too many commands (max 50)")
			}
			var out []string
			for _, name := range names {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				p, err := exec.LookPath(name)
				if err != nil {
					out = append(out, fmt.Sprintf("%s: not found", name))
					continue
				}
				out = append(out, fmt.Sprintf("%s: %s", name, p))
			}
			return strings.Join(out, "\n"), nil
		},
	}
}

// NewPortCheckTool checks whether TCP ports accept connections.
func NewPortCheckTool() *Tool {
	return &Tool{
		Name: "port_check",
		Description: "Check whether one or more TCP ports accept connections. Useful for local dev servers and services. " +
			"Reports open, closed, or the connection error for each port.",
		Parameters: objectSchema(map[string]any{
			"host": propSchema{Type: "string", Description: "Hostname or IP (default 127.0.0.1)."},
			"port": propSchema{Type: "integer", Description: "Single port to check."},
			"ports": map[string]any{
				"type":        "array",
				"description": "Several ports to check.",
				"items":       map[string]any{"type": "integer"},
				"maxItems":    50,
			},
			"timeout_ms": propSchema{Type: "integer", Description: "Per-port timeout in milliseconds (default 500, max 10000)."},
		}),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			host := defaultString(strings.TrimSpace(str(args["host"])), "127.0.0.1")
			var ports []int
			if p := intArg(args, "port", 0); p > 0 {
				ports = append(ports, p)
			}
			if arr, ok := args["ports"].([]any); ok {
				for _, item := range arr {
					switch v := item.(type) {
					case float64:
						ports = append(ports, int(v))
					case int:
						ports = append(ports, v)
					}
				}
			}
			if len(ports) == 0 {
				return "", fmt.Errorf("port or ports is required")
			}
			if len(ports) > 50 {
				return "", fmt.Errorf("too many ports (max 50)")
			}
			timeout := time.Duration(clamp(intArg(args, "timeout_ms", 500), 50, 10000)) * time.Millisecond

			var out []string
			for _, port := range ports {
				if port < 1 || port > 65535 {
					out = append(out, fmt.Sprintf("%s:%d: invalid port", host, port))
					continue
				}
				if err := ctx.Err(); err != nil {
					return "", err
				}
				addr := net.JoinHostPort(host, strconv.Itoa(port))
				conn, err := net.DialTimeout("tcp", addr, timeout)
				if err != nil {
					out = append(out, fmt.Sprintf("%s: closed (%v)", addr, err))
					continue
				}
				_ = conn.Close()
				out = append(out, fmt.Sprintf("%s: open", addr))
			}
			return strings.Join(out, "\n"), nil
		},
	}
}
