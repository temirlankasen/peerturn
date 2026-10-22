package claude

import (
	"strings"
	"testing"
)

func TestRedactionKnownValuesAndPatterns(t *testing.T) {
	known := "core-fixture-only"
	fixtures := []string{
		known, "ghp_" + strings.Repeat("a", 36),
		"github_pat_" + strings.Repeat("b", 70), "AKIA" + strings.Repeat("A", 16),
		"sk-proj-" + strings.Repeat("c", 40), "sk-ant-api03-" + strings.Repeat("d", 40),
		"-----BEGIN " + "OPENSSH PRIVATE KEY-----\nfixture material\n-----END OPENSSH PRIVATE KEY-----",
		"Authorization: Bearer fixture-bearer-value",
		"API_KEY=fixture-only-value", "password: fixture-password-value",
	}
	for _, secret := range fixtures {
		t.Run(secret[:min(len(secret), 16)], func(t *testing.T) {
			output, count, err := redactAnswer("Before "+secret+" after", []string{known})
			if err != nil || count != 1 || strings.Contains(output, secret) || !strings.Contains(output, "[REDACTED:") {
				t.Fatalf("redaction failed: count=%d err=%v", count, err)
			}
			second, secondCount, secondErr := redactAnswer(output, []string{known})
			if secondErr != nil || secondCount != 0 || second != output {
				t.Fatal("redaction is not deterministic and idempotent")
			}
		})
	}
}

func TestRedactionPreservesBenignTextAndDoesNotFetch(t *testing.T) {
	input := "Read README.md and @/tmp/a, https://example.invalid/?q=hello. API_KEY has no value."
	output, count, err := redactAnswer(input, nil)
	if err != nil || count != 0 || output != input {
		t.Fatal("benign text was changed")
	}
	if _, _, err := redactAnswer(string([]byte{0xff}), nil); err == nil {
		t.Fatal("accepted invalid encoding")
	}
}

func TestRedactionBoundsMatchMemoryAndKeepsLongTokensWhole(t *testing.T) {
	if _, _, err := redactAnswer(strings.Repeat("x", 65537), []string{"x"}); err == nil {
		t.Fatal("unbounded secret matches were accepted")
	}
	token := "ghp_" + strings.Repeat("z", 4096)
	output, count, err := redactAnswer(token, nil)
	if err != nil || count != 1 || output != "[REDACTED:github]" {
		t.Fatal("long token was only partly redacted")
	}
}
