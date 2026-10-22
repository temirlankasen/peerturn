// Package protocol defines bounded v1 wire objects without transport or storage.
package protocol

import (
	"encoding/hex"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
)

const (
	// WireVersion prevents accepting a different major protocol by accident.
	WireVersion = 1
	// Suite fixes the HPKE KEM/KDF/AEAD identifiers; there is no negotiation.
	Suite = "0020-0001-0003"
	// MaxPlaintext bounds the complete signed compact object before decryption dispatch.
	MaxPlaintext = 64 * 1024
	// MaxCiphertext bounds the relay-visible encrypted body.
	MaxCiphertext = 96 * 1024
	// MaxPacket includes base64 expansion and a bounded header.
	MaxPacket = 4*MaxCiphertext/3 + 4096
	// MaxSafeInteger keeps the canonical JSON subset interoperable with RFC 8785.
	MaxSafeInteger = 1<<53 - 1
)

// RoutingHeader contains only metadata that a relay may see; every field is AAD.
type RoutingHeader struct {
	// WireVersion prevents interpreting a new major version with old authority rules.
	WireVersion uint16 `json:"wire_version"`
	// Suite pins the single supported HPKE profile without algorithm negotiation.
	Suite string `json:"suite"`
	// FeatureBits requires explicit support before new semantics can affect admission.
	FeatureBits uint64 `json:"feature_bits"`
	// DstMailbox binds ciphertext to one opaque relay destination.
	DstMailbox string `json:"dst_mailbox"`
	// RoutePermitID binds the packet to a caller-authenticated relay permit.
	RoutePermitID string `json:"route_permit_id"`
	// MessageID preserves one immutable retry identity.
	MessageID string `json:"message_id"`
	// RecipientKeyEpoch prevents silently decrypting into a different recipient epoch.
	RecipientKeyEpoch uint64 `json:"recipient_key_epoch"`
	// ExpiresAt bounds retention and admission without exposing message content.
	ExpiresAt int64 `json:"expires_at"`
}

// EncryptedPacket separates encapsulation from ciphertext for standard HPKE.
type EncryptedPacket struct {
	// Header is the complete routing AAD rather than unsigned transport decoration.
	Header RoutingHeader `json:"header"`
	// Enc preserves the original HPKE encapsulation across retries.
	Enc []byte `json:"enc"`
	// Ciphertext keeps signed plaintext off the relay boundary.
	Ciphertext []byte `json:"ciphertext"`
}

// DecodeHeader rejects unsupported features rather than guessing their authority.
func DecodeHeader(data []byte) (RoutingHeader, error) {
	var h RoutingHeader
	if err := codec.Decode(data, 2048, &h); err != nil {
		return h, err
	}
	return h, h.Validate()
}

// Validate checks all routing fields before any expensive cryptographic operation.
func (h RoutingHeader) Validate() error {
	if h.WireVersion != WireVersion || h.Suite != Suite || h.FeatureBits != 0 {
		return fault.New(fault.UnsupportedVersion)
	}
	if !ValidID(h.MessageID) || !ValidToken(h.DstMailbox) || !ValidToken(h.RoutePermitID) ||
		h.RecipientKeyEpoch == 0 || h.RecipientKeyEpoch > MaxSafeInteger || h.ExpiresAt <= 0 || h.ExpiresAt > MaxSafeInteger {
		return fault.New(fault.InvalidEncoding)
	}
	return nil
}

// Canonical produces the sole AAD representation; JSON whitespace is never authority.
func (h RoutingHeader) Canonical() ([]byte, error) {
	if err := h.Validate(); err != nil {
		return nil, err
	}
	return codec.Canonical(h)
}

// DecodePacket bounds allocation before JSON decoding and rejects authority extensions.
func DecodePacket(data []byte) (EncryptedPacket, error) {
	var p EncryptedPacket
	if err := codec.Decode(data, MaxPacket, &p); err != nil {
		return p, err
	}
	return p, p.Validate()
}

// Validate rejects malformed encapsulations and bodies before HPKE is called.
func (p EncryptedPacket) Validate() error {
	if err := p.Header.Validate(); err != nil {
		return err
	}
	if len(p.Ciphertext) > MaxCiphertext {
		return fault.New(fault.TooLarge)
	}
	if len(p.Enc) != 32 || len(p.Ciphertext) < 16 {
		return fault.New(fault.InvalidEncoding)
	}
	return nil
}

// ValidID requires exactly 128 bits of lowercase hex to prevent alternate ID spellings.
func ValidID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if c >= 'A' && c <= 'F' {
			return false
		}
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// ValidToken excludes path separators and unbounded display names from identifiers.
// Dots match the identity/grant grammar so issued identifiers remain usable on wire.
func ValidToken(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' {
			continue
		}
		return false
	}
	return true
}
