package crypto

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func recipientKey(t testing.TB) *ecdh.PrivateKey {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestHPKEFreshContextAndBinding(t *testing.T) {
	key := recipientKey(t)
	plaintext, aad := []byte("private body"), []byte(`{"mailbox":"recipient"}`)
	enc, ct, err := Seal(key.PublicKey(), aad, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	enc2, ct2, err := Seal(key.PublicKey(), aad, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(enc, enc2) || bytes.Equal(ct, ct2) {
		t.Fatal("ephemeral context reused")
	}
	got, err := Open(key, enc, aad, ct)
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Fatalf("open: %v", err)
	}
	wrongAAD := append(bytes.Clone(aad), 'x')
	tampered := bytes.Clone(ct)
	tampered[0] ^= 1
	for _, test := range []struct {
		key          *ecdh.PrivateKey
		enc, aad, ct []byte
	}{
		{key, enc, wrongAAD, ct}, {recipientKey(t), enc, aad, ct}, {key, enc2, aad, ct},
		{key, enc, aad, tampered}, {key, enc, aad, ct[:len(ct)-1]},
		{key, make([]byte, 32), aad, ct}, {key, enc[:len(enc)-1], aad, ct}, {nil, enc, aad, ct},
	} {
		if opened, openErr := Open(test.key, test.enc, test.aad, test.ct); !fault.IsCode(openErr, fault.DecryptionFailed) || opened != nil {
			t.Fatalf("bad ciphertext: %x %v", opened, openErr)
		}
	}
	if _, _, sealErr := Seal(nil, aad, plaintext); !fault.IsCode(sealErr, fault.InvalidEncoding) {
		t.Fatalf("nil key: %v", sealErr)
	}
	p256, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, sealErr := Seal(p256.PublicKey(), aad, plaintext); !fault.IsCode(sealErr, fault.InvalidEncoding) {
		t.Fatalf("wrong sealing curve: %v", sealErr)
	}
	if _, openErr := Open(p256, enc, aad, ct); !fault.IsCode(openErr, fault.DecryptionFailed) {
		t.Fatalf("wrong opening curve: %v", openErr)
	}
}

func TestDigest(t *testing.T) {
	if got := Digest([]byte("abc")); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatal(got)
	}
}
