package invite

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
)

func claimCompact(t testing.TB, fixture inviteFixture) string {
	t.Helper()
	aad, err := packetAAD(fixture.link.ID(), "claim")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := ptcrypto.OpenFor(ptcrypto.ClaimContext, fixture.inviter.key, fixture.claimWire.Packet.Enc, aad, fixture.claimWire.Packet.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	content, err := decodeSignedContent(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	return content.Signed
}

func sealClaim(t testing.TB, fixture inviteFixture, compact string) ClaimPacket {
	t.Helper()
	aad, err := packetAAD(fixture.link.ID(), "claim")
	if err != nil {
		t.Fatal(err)
	}
	enc, ciphertext, err := ptcrypto.SealFor(ptcrypto.ClaimContext, fixture.inviter.key.PublicKey(), aad, canonical(t, signedContent{fixture.claimant.chain, compact}))
	if err != nil {
		t.Fatal(err)
	}
	packet := Packet{1, fixture.link.ID(), enc, ciphertext}
	proof := claimProof{Version: 1, InviteID: fixture.link.ID(), ClaimID: fixture.claim.ClaimID, ReaderKey: base64.RawURLEncoding.EncodeToString(fixture.claim.MailboxKey), ExpiresAt: fixture.claim.ExpiresAt}
	proof.CiphertextHash, err = PacketDigest(packet)
	if err != nil {
		t.Fatal(err)
	}
	return signProof(t, fixture.link, packet, proof)
}

func signProof(t testing.TB, link Link, packet Packet, proof claimProof) ClaimPacket {
	t.Helper()
	signed, err := ptcrypto.Sign(canonical(t, proof), link.ID(), ptcrypto.InviteProofType, ed25519.NewKeyFromSeed(link.secrets.signingSeed[:]))
	if err != nil {
		t.Fatal(err)
	}
	return ClaimPacket{packet, signed}
}

func decodeProof(t testing.TB, packet ClaimPacket) claimProof {
	t.Helper()
	var proof claimProof
	parts := strings.Split(packet.Proof, ".")
	if len(parts) != 3 {
		t.Fatal("malformed synthetic proof")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	if decodeErr := codec.Decode(payload, 1024, &proof); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	return proof
}
