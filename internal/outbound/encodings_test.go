package outbound

import (
	"encoding/base64"
	"slices"
	"testing"
)

func TestSecretEncodingsCoverStoredBinaryAuthority(t *testing.T) {
	value := string([]byte{0xff, 0x00, 0x81, 0xfe, 0x42})
	variants, err := SecretEncodings(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"ff0081fe42", "FF0081FE42", "/wCB/kI=", "_wCB_kI"} {
		if !slices.Contains(variants, expected) {
			t.Fatal("binary authority encoding absent")
		}
	}
	if slices.Contains(variants, value) {
		t.Fatal("invalid UTF-8 literal emitted")
	}
}

func TestSecretEncodingsCoverEscapedAuthority(t *testing.T) {
	variants, err := SecretEncodings("A+/=&é")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"A%2B%2F%3D%26%C3%A9", "%41%2B%2F%3D%26%C3%A9", "%41%2b%2f%3d%26%c3%a9",
		`\u0041\u002b\u002f\u003d\u0026\u00e9`, `\u0041\u002B\u002F\u003D\u0026\u00E9`,
		"&#65;&#43;&#47;&#61;&#38;&#233;", "&#x41;&#x2b;&#x2f;&#x3d;&#x26;&#xe9;",
		base64.RawStdEncoding.EncodeToString([]byte("A+/=&é")),
	} {
		if !slices.Contains(variants, expected) {
			t.Fatal("escaped authority encoding absent")
		}
	}
	if !slices.IsSorted(variants) || len(slices.Compact(slices.Clone(variants))) != len(variants) {
		t.Fatal("variants not deterministic and unique")
	}
}

func TestSecretEncodingsRejectInvalidBounds(t *testing.T) {
	for _, value := range []string{"", string(make([]byte, 65537))} {
		if variants, err := SecretEncodings(value); err == nil || len(variants) != 0 {
			t.Fatal("invalid secret accepted")
		}
	}
}
