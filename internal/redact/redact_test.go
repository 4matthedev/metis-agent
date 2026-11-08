package redact

import (
	"strings"
	"testing"
)

func TestStringRedactsCredentialShapes(t *testing.T) {
	cases := []string{
		`api_key = "sk-proj-abcdefghijklmnopqrstuvwx"`,
		`Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345`,
		`token=ghp_abcdefghijklmnopqrstuvwxyz0123456789`,
		`aws=AKIAABCDEFGHIJKLMNOP`,
		`secret: superSecretValue123`,
		`-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAA
-----END OPENSSH PRIVATE KEY-----`,
	}
	for _, in := range cases {
		out := String(in)
		if strings.Contains(out, "sk-proj-abcdefghijklmnopqrstuvwx") ||
			strings.Contains(out, "abcdefghijklmnopqrstuvwxyz012345") ||
			strings.Contains(out, "ghp_abcdefghijklmnopqrstuvwxyz") ||
			strings.Contains(out, "AKIAABCDEFGHIJKLMNOP") ||
			strings.Contains(out, "superSecretValue123") ||
			strings.Contains(out, "b3BlbnNzaC1rZXktdjEAAAAA") {
			t.Fatalf("secret survived redaction: %q", out)
		}
	}
}

func TestStringLeavesOrdinaryText(t *testing.T) {
	in := "password: ask the user\nconst apiKeyName = \"METIS_API_KEY\"\n"
	if got := String(in); got != in {
		t.Fatalf("ordinary text changed:\n got: %q\nwant: %q", got, in)
	}
}

func TestLooksSecret(t *testing.T) {
	for _, name := range []string{"OPENAI_API_KEY", "GITHUB_TOKEN", "DB_PASSWORD", "AWS_SECRET"} {
		if !LooksSecret(name) {
			t.Fatalf("%s should look secret", name)
		}
	}
	for _, name := range []string{"HOME", "PATH", "LANG"} {
		if LooksSecret(name) {
			t.Fatalf("%s should not look secret", name)
		}
	}
}
