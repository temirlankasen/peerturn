package crypto

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestJWSRejectsIdentityKeyForgery(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA","kid":"k","typ":"peerturn-message-v1"}`))
	public := make(ed25519.PublicKey, ed25519.PublicKeySize)
	public[0] = 1
	// R=B and S=1 satisfy the verification equation for the identity public
	// key independently of the message; this must fail the key profile first.
	signature := fromHex(t, "58666666666666666666666666666666666666666666666666666666666666660100000000000000000000000000000000000000000000000000000000000000")
	for _, payload := range []string{"arbitrary authority", "different authority"} {
		input := header + "." + base64.RawURLEncoding.EncodeToString([]byte(payload))
		if !ed25519.Verify(public, []byte(input), signature) {
			t.Fatal("identity-key exploit fixture no longer reaches stdlib verifier")
		}
		compact := input + "." + base64.RawURLEncoding.EncodeToString(signature)
		if got, err := Verify(compact, "k", MessageType, public); !fault.IsCode(err, fault.InvalidSignature) || got != nil {
			t.Fatalf("identity key forged payload accepted: %q %v", got, err)
		}
	}
}

func TestJWSRejectsDottedCompactBeforeAllocation(t *testing.T) {
	public := signingPublic(t, signingKey(t))
	compact := strings.Repeat(".", maxCompactBytes)
	if _, err := Verify(compact, "k", MessageType, public); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatal(err)
	}
	measurement := testing.Benchmark(func(b *testing.B) {
		for b.Loop() {
			if _, err := Verify(compact, "k", MessageType, public); !fault.IsCode(err, fault.InvalidEncoding) {
				b.Fatalf("malformed compact: %v", err)
			}
		}
	})
	if allocated := measurement.AllocedBytesPerOp(); allocated > 1024 {
		t.Fatalf("malformed compact allocated %d bytes per rejection", allocated)
	}
	if _, err := Verify(compact+".", "k", MessageType, public); !fault.IsCode(err, fault.TooLarge) {
		t.Fatal(err)
	}
}

func TestJWSRejectsNoncanonicalSigningKey(t *testing.T) {
	public := bytes.Repeat([]byte{0xff}, ed25519.PublicKeySize)
	for _, sign := range []byte{0x7f, 0xff} {
		public[31] = sign
		if _, err := Verify("", "k", MessageType, public); !fault.IsCode(err, fault.InvalidSignature) {
			t.Fatalf("noncanonical key not rejected before parse: %v", err)
		}
	}
}
