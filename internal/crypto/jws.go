// Package crypto implements the fixed PeerTurn HPKE and Ed25519 JWS profile.
package crypto

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
)

// Protected JWS types bind signatures to one PeerTurn protocol object class.
const (
	// MessageType prevents message signatures from authorizing other object classes.
	MessageType = "peerturn-message-v1"
	// DeviceType binds root-issued signatures specifically to device certificates.
	DeviceType = "peerturn-device-v1"
	// SessionType binds device-issued signatures specifically to session certificates.
	SessionType = "peerturn-session-v1"
	// GrantType separates recipient-issued authority from ordinary signed content.
	GrantType = "peerturn-grant-v1"
	// RotationType separates key-transition proofs from other root signatures.
	RotationType = "peerturn-rotation-v1"
	// InviteType keeps an invitation signature distinct from its enrollment claims.
	InviteType = "peerturn-invite-v1"
	// ClaimType prevents an enrollment claim from being treated as an invitation.
	ClaimType = "peerturn-claim-v1"
	// InviteProofType separates capability-possession proof from claimed identity.
	InviteProofType = "peerturn-invite-proof-v1"
)

const maxCompactBytes = 100 * 1024

type protected struct {
	Alg  string `json:"alg"`
	KID  string `json:"kid"`
	Type string `json:"typ"`
}

// Sign authenticates the exact payload bytes without parsing or rewriting them.
func Sign(payload []byte, kid, typ string, key ed25519.PrivateKey) (string, error) {
	if len(key) != ed25519.PrivateKeySize || !validKID(kid) || !validType(typ) {
		return "", fault.New(fault.InvalidEncoding)
	}
	if len(payload) > 64*1024 {
		return "", fault.New(fault.TooLarge)
	}
	header, err := codec.Canonical(protected{Alg: "EdDSA", KID: kid, Type: typ})
	if err != nil {
		return "", err
	}
	return signEncoded(base64.RawURLEncoding.EncodeToString(header), payload, key), nil
}

func signEncoded(header string, payload []byte, key ed25519.PrivateKey) string {
	input := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	signature := ed25519.Sign(key, []byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// Verify pins the key, type and EdDSA algorithm, returning original authenticated
// payload bytes for schema parsing by the caller only after verification.
func Verify(compact, kid, typ string, key ed25519.PublicKey) ([]byte, error) {
	if len(compact) > maxCompactBytes {
		return nil, fault.New(fault.TooLarge)
	}
	if !ValidSigningKey(key) || !validKID(kid) || !validType(typ) {
		return nil, fault.New(fault.InvalidSignature)
	}
	if strings.Count(compact, ".") != 2 {
		return nil, fault.New(fault.InvalidEncoding)
	}
	parts := strings.Split(compact, ".")
	header, err := decodeSegment(parts[0])
	if err != nil {
		return nil, err
	}
	var fields protected
	if err := codec.Decode(header, 2048, &fields); err != nil {
		return nil, err
	}
	if fields.Alg != "EdDSA" || fields.KID != kid || fields.Type != typ {
		return nil, fault.New(fault.InvalidSignature)
	}
	if !verifyEncoded(parts, key) {
		return nil, fault.New(fault.InvalidSignature)
	}
	return decodeSegment(parts[1])
}

func verifyEncoded(parts []string, key ed25519.PublicKey) bool {
	if len(parts) != 3 || !ValidSigningKey(key) {
		return false
	}
	signature, err := decodeSegment(parts[2])
	return err == nil && len(signature) == ed25519.SignatureSize && ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), signature)
}

func decodeSegment(segment string) ([]byte, error) {
	for _, r := range segment {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !valid {
			return nil, fault.New(fault.InvalidEncoding)
		}
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(segment)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != segment {
		return nil, fault.New(fault.InvalidEncoding)
	}
	return decoded, nil
}

func validKID(kid string) bool { return len(kid) > 0 && len(kid) <= 256 && utf8.ValidString(kid) }

func validType(typ string) bool {
	switch typ {
	case MessageType, DeviceType, SessionType, GrantType, RotationType, InviteType, ClaimType, InviteProofType:
		return true
	default:
		return false
	}
}
