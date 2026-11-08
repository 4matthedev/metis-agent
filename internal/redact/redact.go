// Package redact removes common credential shapes from text before it is
// returned to the model or written to a transcript.
package redact

import (
	"regexp"
	"strings"
)

// placeholder is what secret values are replaced with.
const placeholder = "<redacted>"

var patterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// PEM private keys (RSA, EC, OPENSSH, DSA, PGP).
	{
		regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
		"[redacted private key]",
	},
	// Authorization headers and key/value assignments. The value must look
	// like an opaque token, so prose such as "password: ask the user" is left
	// alone.
	{
		regexp.MustCompile(`(?i)\b((?:api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret|secret[_-]?key|secret|password|passwd|authorization)\s*[:=]\s*["']?)([A-Za-z0-9._~+/=-]{8,})`),
		`${1}` + placeholder,
	},
	{
		regexp.MustCompile(`(?i)\b(bearer\s+)([A-Za-z0-9._~+/=-]{16,})`),
		`${1}` + placeholder,
	},
	// Well-known token prefixes.
	{
		regexp.MustCompile(`\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{16,}\b`),
		placeholder,
	},
	{
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),
		placeholder,
	},
	{
		regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`),
		placeholder,
	},
	{
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		placeholder,
	},
	{
		regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
		placeholder,
	},
	{
		regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}\b`),
		placeholder,
	},
}

// String replaces recognizable credentials in s with a placeholder. It is
// deliberately conservative: when in doubt it leaves text unchanged rather
// than mangling source code or documentation.
func String(s string) string {
	if s == "" {
		return s
	}
	for _, p := range patterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

// LooksSecret reports whether a variable name conventionally holds a secret.
// It is used by env_get and by the redactor's callers for name-based checks.
func LooksSecret(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE", "AUTH"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}
