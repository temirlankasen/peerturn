package crypto

import (
	"crypto/ecdh"
	"crypto/hpke"
	"crypto/sha256"
	"encoding/hex"

	"github.com/temirlankasen/peerturn/internal/fault"
)

const envelopeInfo = "peerturn/v1/envelope"

// Context chooses one fixed HPKE info value for a PeerTurn object class.
type Context uint8

const (
	// EnvelopeContext binds ordinary messages to the envelope HPKE profile.
	EnvelopeContext Context = iota + 1
	// DescriptorContext keeps invitation descriptors separate from messages.
	DescriptorContext
	// ClaimContext keeps enrollment claims separate from messages and descriptors.
	ClaimContext
)

// Seal creates a fresh RFC9180 Base-mode context for exactly one message, with
// DHKEM(X25519, HKDF-SHA256), HKDF-SHA256 and ChaCha20Poly1305.
func Seal(recipient *ecdh.PublicKey, aad, plaintext []byte) (enc, ciphertext []byte, err error) {
	return SealFor(EnvelopeContext, recipient, aad, plaintext)
}

// SealFor encrypts one object using the fixed HPKE profile for its class.
func SealFor(objectContext Context, recipient *ecdh.PublicKey, aad, plaintext []byte) (enc, ciphertext []byte, err error) {
	info := contextInfo(objectContext)
	if info == "" || recipient == nil || recipient.Curve() != ecdh.X25519() {
		return nil, nil, fault.New(fault.InvalidEncoding)
	}
	key, err := hpke.DHKEM(ecdh.X25519()).NewPublicKey(recipient.Bytes())
	if err != nil {
		return nil, nil, fault.New(fault.InvalidEncoding)
	}
	enc, sender, err := hpke.NewSender(key, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte(info))
	if err != nil {
		return nil, nil, fault.New(fault.InvalidEncoding)
	}
	ciphertext, err = sender.Seal(aad, plaintext)
	if err != nil {
		return nil, nil, fault.New(fault.InvalidEncoding)
	}
	return enc, ciphertext, nil
}

// Open authenticates and decrypts one message in a fresh context, returning no
// plaintext when the recipient, encapsulation, info, AAD or ciphertext differs.
func Open(recipient *ecdh.PrivateKey, enc, aad, ciphertext []byte) ([]byte, error) {
	return OpenFor(EnvelopeContext, recipient, enc, aad, ciphertext)
}

// OpenFor authenticates one object using its expected HPKE object class.
func OpenFor(objectContext Context, recipient *ecdh.PrivateKey, enc, aad, ciphertext []byte) ([]byte, error) {
	info := contextInfo(objectContext)
	if info == "" || recipient == nil || recipient.Curve() != ecdh.X25519() {
		return nil, fault.New(fault.DecryptionFailed)
	}
	key, err := hpke.DHKEM(ecdh.X25519()).NewPrivateKey(recipient.Bytes())
	if err != nil {
		return nil, fault.New(fault.DecryptionFailed)
	}
	context, err := hpke.NewRecipient(enc, key, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte(info))
	if err != nil {
		return nil, fault.New(fault.DecryptionFailed)
	}
	plaintext, err := context.Open(aad, ciphertext)
	if err != nil {
		return nil, fault.New(fault.DecryptionFailed)
	}
	return plaintext, nil
}

func contextInfo(context Context) string {
	switch context {
	case EnvelopeContext:
		return envelopeInfo
	case DescriptorContext:
		return "peerturn/v1/invite-descriptor"
	case ClaimContext:
		return "peerturn/v1/invite-claim"
	default:
		return ""
	}
}

// Digest returns a lowercase hexadecimal SHA-256 digest.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
