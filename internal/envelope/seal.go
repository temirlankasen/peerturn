package envelope

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"encoding/json"

	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// SignedMessage binds the complete relay-visible destination to its signed content.
// Certificates and the grant are resolved from pinned local state, never a remote kid.
type SignedMessage struct {
	// Header binds the sender signature to the same complete routing AAD.
	Header protocol.RoutingHeader `json:"header"`
	// Message carries exact signed content without resolving text into effects.
	Message protocol.MessagePayload `json:"message"`
}

// Seal signs exact payload bytes and uses one fresh HPKE context for this message.
// Successful sealing does not establish recipient acceptance or current grant authority.
func Seal(h protocol.RoutingHeader, m protocol.MessagePayload, key ed25519.PrivateKey, recipient *ecdh.PublicKey) (protocol.EncryptedPacket, error) {
	p := protocol.EncryptedPacket{Header: h}
	aad, err := h.Canonical()
	if err != nil {
		return p, err
	}
	if checkErr := m.Validate(); checkErr != nil {
		return p, checkErr
	}
	if h.MessageID != m.MessageID || h.ExpiresAt != m.ExpiresAt {
		return p, fault.New(fault.AudienceMismatch)
	}
	if len(key) != ed25519.PrivateKeySize {
		return p, fault.New(fault.InvalidSignature)
	}
	pub, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		return p, fault.New(fault.InvalidSignature)
	}
	raw, err := json.Marshal(SignedMessage{Header: h, Message: m})
	if err != nil {
		return p, fault.New(fault.InvalidEncoding)
	}
	compact, err := peercrypto.Sign(raw, identity.RootFingerprint(pub), peercrypto.MessageType, key)
	if err != nil {
		return p, err
	}
	if len(compact) > protocol.MaxPlaintext {
		return p, fault.New(fault.TooLarge)
	}
	p.Enc, p.Ciphertext, err = peercrypto.Seal(recipient, aad, []byte(compact))
	return p, err
}
