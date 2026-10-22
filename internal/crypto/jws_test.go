package crypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func signingKey(t testing.TB) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func signingPublic(t testing.TB, key ed25519.PrivateKey) ed25519.PublicKey {
	t.Helper()
	public, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("unexpected public key type")
	}
	return public
}

func TestJWSAuthenticatesExactPayloadBeforeJSON(t *testing.T) {
	key := signingKey(t)
	public := signingPublic(t, key)
	for _, payload := range [][]byte{[]byte(` {"z": 2, "a":1} `), []byte("not JSON\x00\xff"), {}, bytes.Repeat([]byte("x"), 64*1024)} {
		compact, err := Sign(payload, "key-1", MessageType, key)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Verify(compact, "key-1", MessageType, public)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("payload changed: %v", err)
		}
		if _, err := Verify(compact, "key-2", MessageType, public); !fault.IsCode(err, fault.InvalidSignature) {
			t.Fatal(err)
		}
		if _, err := Verify(compact, "key-1", GrantType, public); !fault.IsCode(err, fault.InvalidSignature) {
			t.Fatal(err)
		}
		if _, err := Verify(compact, "key-1", MessageType, signingPublic(t, signingKey(t))); !fault.IsCode(err, fault.InvalidSignature) {
			t.Fatalf("wrong key: %v", err)
		}
	}
}

func TestJWSRejectsHeaderConfusion(t *testing.T) {
	key := signingKey(t)
	public := signingPublic(t, key)
	headers := []struct {
		header string
		code   fault.Code
	}{
		{`{"alg":"none","kid":"k","typ":"peerturn-message-v1"}`, fault.InvalidSignature},
		{`{"alg":"HS256","kid":"k","typ":"peerturn-message-v1"}`, fault.InvalidSignature},
		{`{"alg":"EdDSA","kid":"k","typ":"peerturn-message-v1","jku":"https://example.invalid"}`, fault.InvalidEncoding},
		{`{"alg":"EdDSA","kid":"k","typ":"peerturn-message-v1","x5u":"https://example.invalid"}`, fault.InvalidEncoding},
		{`{"alg":"EdDSA","kid":"k","typ":"peerturn-message-v1","jwk":{}}`, fault.InvalidEncoding},
		{`{"alg":"EdDSA","kid":"k","typ":"peerturn-message-v1","crit":[]}`, fault.InvalidEncoding},
		{`{"alg":"EdDSA","kid":"k","typ":"peerturn-message-v1","b64":false}`, fault.InvalidEncoding},
		{`{"alg":"EdDSA","kid":"k","typ":"peerturn-message-v1","alg":"EdDSA"}`, fault.InvalidEncoding},
		{`{"ALG":"EdDSA","kid":"k","typ":"peerturn-message-v1"}`, fault.InvalidEncoding},
		{`{"alg":"EdDSA","kid":"k"}`, fault.InvalidSignature},
		{`{"alg":"EdDSA","typ":"peerturn-message-v1"}`, fault.InvalidSignature},
		{`{"alg":"EdDSA","kid":null,"typ":"peerturn-message-v1"}`, fault.InvalidEncoding},
	}
	for _, tc := range headers {
		compact := signEncoded(base64.RawURLEncoding.EncodeToString([]byte(tc.header)), []byte(`{}`), key)
		if _, err := Verify(compact, "k", MessageType, public); !fault.IsCode(err, tc.code) {
			t.Fatalf("header %s: want %s, got %v", tc.header, tc.code, err)
		}
	}
}

func TestJWSRejectsMalformedCompactAndKeys(t *testing.T) {
	key := signingKey(t)
	public := signingPublic(t, key)
	valid, err := Sign([]byte("a"), "k", MessageType, key)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(valid, ".")
	invalid := []struct {
		compact string
		code    fault.Code
	}{
		{"", fault.InvalidEncoding}, {".", fault.InvalidEncoding}, {"..", fault.InvalidEncoding},
		{valid + ".", fault.InvalidEncoding}, {strings.Repeat("a", 100*1024+1), fault.TooLarge},
		{parts[0] + "=." + parts[1] + "." + parts[2], fault.InvalidEncoding},
		{parts[0] + "." + parts[1] + "=." + parts[2], fault.InvalidSignature},
		{parts[0] + "." + parts[1] + "." + parts[2] + "=", fault.InvalidSignature},
		{strings.Replace(valid, ".", ".\n", 1), fault.InvalidSignature},
		{parts[0] + ".YR." + parts[2], fault.InvalidSignature},
		{parts[0] + ".YQ." + parts[2][:len(parts[2])-1] + "B", fault.InvalidSignature},
	}
	for i, tc := range invalid {
		if _, err := Verify(tc.compact, "k", MessageType, public); !fault.IsCode(err, tc.code) {
			t.Fatalf("malformed JWS case %d: want %s, got %v", i, tc.code, err)
		}
	}
	if _, err := Sign([]byte("x"), "k", MessageType, nil); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatalf("nil signing key: %v", err)
	}
	if _, err := Sign([]byte("x"), "", MessageType, key); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatalf("empty kid: %v", err)
	}
	if _, err := Sign([]byte("x"), "k", "arbitrary-type", key); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatalf("unknown type: %v", err)
	}
	if _, err := Sign(bytes.Repeat([]byte("a"), 100*1024), "k", MessageType, key); !fault.IsCode(err, fault.TooLarge) {
		t.Fatal(err)
	}
	if _, err := Verify(valid, "k", MessageType, nil); !fault.IsCode(err, fault.InvalidSignature) {
		t.Fatalf("nil verification key: %v", err)
	}
}

func FuzzVerify(f *testing.F) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{42}, ed25519.SeedSize))
	public := signingPublic(f, key)
	valid, err := Sign([]byte(`{"ok":true}`), "fuzz", MessageType, key)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{valid, "", "..", "eyJhbGciOiJub25lIn0.e30."} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, compact string) {
		payload, err := Verify(compact, "fuzz", MessageType, public)
		if err != nil {
			return
		}
		parts := strings.Split(compact, ".")
		if len(parts) != 3 || !ed25519.Verify(public, []byte(parts[0]+"."+parts[1]), mustBase64(t, parts[2])) {
			t.Fatal("accepted invalid signature")
		}
		if !bytes.Equal(payload, mustBase64(t, parts[1])) {
			t.Fatal("payload changed")
		}
	})
}

func mustBase64(t testing.TB, encoded string) []byte {
	t.Helper()
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
