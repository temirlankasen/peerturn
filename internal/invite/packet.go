package invite

import (
	"bytes"

	"github.com/temirlankasen/peerturn/internal/codec"
	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
)

// MaxPacketBytes bounds encrypted invitation and claim objects before decoding.
const MaxPacketBytes = 64 << 10

// Packet carries one HPKE ciphertext; it reveals no scopes or session names.
type Packet struct {
	// Version fixes the encrypted packet profile and prevents downgrade.
	Version int `json:"wire_version"`
	// InviteID is the random public slot bound into HPKE authenticated data.
	InviteID string `json:"invite_id"`
	// Enc carries the fresh standard HPKE encapsulation for this packet.
	Enc []byte `json:"enc"`
	// Ciphertext hides the signed policy or claim from the relay.
	Ciphertext []byte `json:"ciphertext"`
}

// DecodePacket rejects duplicate, unknown, oversized, and malformed fields.
func DecodePacket(data []byte) (Packet, error) {
	var packet Packet
	if err := codec.Decode(data, MaxPacketBytes, &packet); err != nil {
		return Packet{}, err
	}
	if err := validatePacket(packet); err != nil {
		return Packet{}, err
	}
	return packet, nil
}

// PacketDigest hashes canonical encrypted packet bytes, including encapsulation.
// The URL mh pin uses this digest, not a self-referential signed descriptor hash.
func PacketDigest(packet Packet) (string, error) {
	if err := validatePacket(packet); err != nil {
		return "", err
	}
	data, err := codec.Canonical(packet)
	if err != nil {
		return "", err
	}
	return ptcrypto.Digest(data), nil
}

func validatePacket(packet Packet) error {
	if packet.Version != 1 {
		return fault.New(fault.UnsupportedVersion)
	}
	if !encodedBytes(packet.InviteID, 16) || len(packet.Enc) != 32 || len(packet.Ciphertext) < 16 {
		return fault.New(fault.InvalidInvite)
	}
	if len(packet.Ciphertext) > MaxPacketBytes/2 {
		return fault.New(fault.TooLarge)
	}
	return nil
}

func packetAAD(id, kind string) ([]byte, error) {
	return codec.Canonical(struct {
		Version  int    `json:"wire_version"`
		InviteID string `json:"invite_id"`
		Kind     string `json:"kind"`
	}{1, id, kind})
}

// The outer chain is only a key-selection hint until its pinned signatures verify.
// Signed JSON is never parsed before the exact compact payload verifies.
type signedContent struct {
	// Chain supplies a certificate hint to authenticate before inspecting signed JSON.
	Chain identity.Chain `json:"chain"`
	// Signed holds the exact compact signature whose bytes must verify first.
	Signed string `json:"signed"`
}

func decodeSignedContent(data []byte) (signedContent, error) {
	var content signedContent
	if err := codec.Decode(data, MaxPacketBytes/2, &content); err != nil {
		return signedContent{}, err
	}
	if len(content.Signed) == 0 || len(content.Signed) > MaxPacketBytes/2 {
		return signedContent{}, fault.New(fault.InvalidInvite)
	}
	return content, nil
}

func verifySignedContent(data []byte, pin, typ string, now int64) ([]byte, identity.Chain, identity.SessionCertificate, error) {
	content, err := decodeSignedContent(data)
	if err != nil {
		return nil, identity.Chain{}, identity.SessionCertificate{}, err
	}
	return verifyContent(content, pin, typ, now)
}

func verifyContent(content signedContent, pin, typ string, now int64) ([]byte, identity.Chain, identity.SessionCertificate, error) {
	cert, err := identity.VerifyChain(content.Chain, pin, now)
	if err != nil {
		return nil, identity.Chain{}, identity.SessionCertificate{}, err
	}
	payload, err := ptcrypto.Verify(content.Signed, identity.RootFingerprint(cert.SigningKey), typ, cert.SigningKey)
	if err != nil {
		return nil, identity.Chain{}, identity.SessionCertificate{}, err
	}
	return payload, content.Chain, cert, nil
}

func equalChain(a, b identity.Chain) bool {
	return bytes.Equal(a.RootPublicKey, b.RootPublicKey) && a.Device == b.Device && a.Session == b.Session
}
