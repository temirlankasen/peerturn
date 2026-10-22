package crypto

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hpke"
	"encoding/hex"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestRFC9180AppendixA21PublishedCiphertext(t *testing.T) {
	var vector struct {
		Source      string `json:"source"`
		Description string `json:"description"`
		KEM         uint16 `json:"kem_id"`
		KDF         uint16 `json:"kdf_id"`
		AEAD        uint16 `json:"aead_id"`
		Info        string `json:"info"`
		Public      string `json:"recipient_public"`
		Private     string `json:"recipient_private"`
		Enc         string `json:"enc"`
		Plaintext   string `json:"plaintext"`
		AAD         string `json:"aad"`
		Ciphertext  string `json:"ciphertext"`
	}
	readVector(t, "crypto-rfc9180.json", &vector)
	kem := hpke.DHKEM(ecdh.X25519())
	kdf, aead := hpke.HKDFSHA256(), hpke.ChaCha20Poly1305()
	if kem.ID() != vector.KEM || kdf.ID() != vector.KDF || aead.ID() != vector.AEAD {
		t.Fatal("suite mismatch")
	}
	key, err := kem.NewPrivateKey(fromHex(t, vector.Private))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key.PublicKey().Bytes(), fromHex(t, vector.Public)) {
		t.Fatal("recipient public key mismatch")
	}
	// Decrypt the published ciphertext using the complete stdlib HPKE context,
	// rather than reproducing any part of the RFC key schedule in test code.
	recipient, err := hpke.NewRecipient(fromHex(t, vector.Enc), key, kdf, aead, fromHex(t, vector.Info))
	if err != nil {
		t.Fatal(err)
	}
	got, err := recipient.Open(fromHex(t, vector.AAD), fromHex(t, vector.Ciphertext))
	if err != nil || !bytes.Equal(got, fromHex(t, vector.Plaintext)) {
		t.Fatalf("RFC9180 decrypt: %x %v", got, err)
	}
}

func TestHPKEProfileInteroperatesWithStdlib(t *testing.T) {
	key := recipientKey(t)
	plain, aad := []byte("exact signed compact"), []byte("routing AAD")
	enc, ciphertext, err := Seal(key.PublicKey(), aad, plain)
	if err != nil {
		t.Fatal(err)
	}
	private, err := hpke.DHKEM(ecdh.X25519()).NewPrivateKey(key.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := hpke.NewRecipient(enc, private, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte("peerturn/v1/envelope"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := recipient.Open(aad, ciphertext)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("profile decrypt: %v", err)
	}
	wrongInfo, err := hpke.NewRecipient(enc, private, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte("wrong info"))
	if err != nil {
		t.Fatal(err)
	}
	if _, openErr := wrongInfo.Open(aad, ciphertext); openErr == nil {
		t.Fatal("info not bound")
	}
	public, err := hpke.DHKEM(ecdh.X25519()).NewPublicKey(key.PublicKey().Bytes())
	if err != nil {
		t.Fatal(err)
	}
	enc, sender, err := hpke.NewSender(public, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte("peerturn/v1/envelope"))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err = sender.Seal(aad, plain)
	if err != nil {
		t.Fatal(err)
	}
	got, err = Open(key, enc, aad, ciphertext)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("profile open: %v", err)
	}
}

func TestRFC8037AppendixA4ExactCompact(t *testing.T) {
	var vector struct {
		Source      string `json:"source"`
		Description string `json:"description"`
		Seed        string `json:"seed"`
		Public      string `json:"public_key"`
		Payload     string `json:"payload"`
		Protected   string `json:"protected"`
		Compact     string `json:"compact"`
	}
	readVector(t, "crypto-rfc8037.json", &vector)
	key := ed25519.NewKeyFromSeed(fromHex(t, vector.Seed))
	public := ed25519.PublicKey(fromHex(t, vector.Public))
	got := signEncoded(vector.Protected, []byte(vector.Payload), key)
	if got != vector.Compact {
		t.Fatalf("RFC8037 signature mismatch: %s", got)
	}
	if !verifyEncoded(strings.Split(vector.Compact, "."), public) {
		t.Fatal("RFC8037 verification failed")
	}
	if _, err := Verify(vector.Compact, "key", MessageType, public); !fault.IsCode(err, fault.InvalidSignature) {
		t.Fatalf("raw RFC header: %v", err)
	}
}

func readVector(t *testing.T, name string, out any) {
	t.Helper()
	data, err := fs.ReadFile(os.DirFS("../../api/fixtures/v1"), name)
	if err != nil {
		t.Fatal(err)
	}
	if err := codec.Decode(data, 8192, out); err != nil {
		t.Fatal(err)
	}
}

func fromHex(t *testing.T, encoded string) []byte {
	t.Helper()
	data, err := hex.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
