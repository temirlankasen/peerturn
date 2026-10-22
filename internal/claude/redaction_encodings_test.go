package claude

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func TestRedactionKnownAuthorityEncodings(t *testing.T) {
	value := "fixture-authority-only+/="
	for name, encoded := range map[string]string{
		"base64":    base64.StdEncoding.EncodeToString([]byte(value)),
		"base64url": base64.RawURLEncoding.EncodeToString([]byte(value)),
		"hex":       hex.EncodeToString([]byte(value)),
		"upper_hex": strings.ToUpper(hex.EncodeToString([]byte(value))),
	} {
		t.Run(name, func(t *testing.T) {
			answer, count, err := redactAnswer("before "+encoded+" after", []string{value})
			if err != nil || count != 1 || answer != "before [REDACTED:known] after" {
				t.Fatalf("known authority escaped: count=%d err=%v", count, err)
			}
		})
	}
}
