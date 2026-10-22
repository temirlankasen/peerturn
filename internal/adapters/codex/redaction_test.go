package codex

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestRedactAnswerDeterministicKnownValuesAndPatterns(t *testing.T) {
	input := "Core fixture-authority-value, token " + "sk-" + "test_1234567890abcdefghijk, file @/tmp/private. https://example.invalid unchanged"
	secrets := []KnownSecret{{Class: "authority", Value: "fixture-authority-value"}, {Class: "short", Value: "fixture-authority"}}
	got, err := RedactAnswer(input, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if got.Count != 3 || strings.Contains(got.Text, "fixture-authority") || strings.Contains(got.Text, "sk-test") || strings.Contains(got.Text, "@/tmp") {
		t.Fatalf("result %+v", got)
	}
	if !strings.Contains(got.Text, "https://example.invalid unchanged") {
		t.Fatal("literal URL text changed or fetched")
	}
	again, err := RedactAnswer(input, []KnownSecret{secrets[1], secrets[0]})
	if err != nil || again != got {
		t.Fatalf("nondeterministic result %+v error %v", again, err)
	}
}

func TestRedactAnswerKnownBase64Value(t *testing.T) {
	value := "fixture-context-secret"
	encoded := base64.StdEncoding.EncodeToString([]byte(value))
	got, err := RedactAnswer(encoded, []KnownSecret{{Class: "history", Value: value}})
	if err != nil || got.Text != "[REDACTED:history]" || got.Count != 1 {
		t.Fatalf("encoded known value escaped redaction: %+v error %v", got, err)
	}
}

func TestRedactAnswerOverlappingKnownValues(t *testing.T) {
	got, err := RedactAnswer("aaaaa", []KnownSecret{{Class: "authority", Value: "aaa"}})
	if err != nil || got.Text != "[REDACTED:authority]" || got.Count != 1 {
		t.Fatalf("overlap exposed suffix: %+v error %v", got, err)
	}
}

func TestRedactAnswerRejectsMalformedOrOversizedOutput(t *testing.T) {
	for _, input := range []string{string([]byte{0xff}), strings.Repeat("x", 16385)} {
		if got, err := RedactAnswer(input, nil); err == nil || got.Text != "" {
			t.Fatalf("unsafe output %+v error %v", got, err)
		}
	}
}
