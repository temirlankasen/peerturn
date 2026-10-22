package crypto

import (
	"bytes"
	"crypto/ecdh"
	"crypto/hpke"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestHPKEObjectContextsCannotCrossDecrypt(t *testing.T) {
	key := recipientKey(t)
	aad, plain := []byte("identical AAD"), []byte("typed plaintext")
	contexts := []Context{EnvelopeContext, DescriptorContext, ClaimContext}
	for _, sealedContext := range contexts {
		enc, ciphertext, err := SealFor(sealedContext, key.PublicKey(), aad, plain)
		if err != nil {
			t.Fatal(err)
		}
		for _, openedContext := range contexts {
			got, openErr := OpenFor(openedContext, key, enc, aad, ciphertext)
			if sealedContext == openedContext {
				if openErr != nil || !bytes.Equal(got, plain) {
					t.Errorf("context %d round trip: %v", sealedContext, openErr)
				}
			} else if !fault.IsCode(openErr, fault.DecryptionFailed) || got != nil {
				t.Errorf("context %d decrypted in %d: %v", sealedContext, openedContext, openErr)
			}
		}
	}
	for _, invalid := range []Context{0, 255} {
		if enc, ciphertext, err := SealFor(invalid, key.PublicKey(), aad, plain); !fault.IsCode(err, fault.InvalidEncoding) || enc != nil || ciphertext != nil {
			t.Errorf("unknown sealing context %d: %v", invalid, err)
		}
		if got, err := OpenFor(invalid, key, nil, aad, nil); !fault.IsCode(err, fault.DecryptionFailed) || got != nil {
			t.Errorf("unknown opening context %d: %v", invalid, err)
		}
	}
}

func TestHPKEObjectInfoInteroperatesWithStdlib(t *testing.T) {
	key := recipientKey(t)
	private, err := hpke.DHKEM(ecdh.X25519()).NewPrivateKey(key.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		context Context
		info    string
	}{
		{EnvelopeContext, "peerturn/v1/envelope"},
		{DescriptorContext, "peerturn/v1/invite-descriptor"},
		{ClaimContext, "peerturn/v1/invite-claim"},
	} {
		enc, ciphertext, sealErr := SealFor(tc.context, key.PublicKey(), []byte("aad"), []byte("body"))
		if sealErr != nil {
			t.Fatal(sealErr)
		}
		recipient, recipientErr := hpke.NewRecipient(enc, private, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte(tc.info))
		if recipientErr != nil {
			t.Fatal(recipientErr)
		}
		if got, openErr := recipient.Open([]byte("aad"), ciphertext); openErr != nil || string(got) != "body" {
			t.Errorf("stdlib info %s: %v", tc.info, openErr)
		}
	}
}
