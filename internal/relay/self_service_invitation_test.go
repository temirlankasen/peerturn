package relay

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/invite"
)

type selfServiceHTTPInvite struct {
	id      string
	proof   ed25519.PrivateKey
	packet  invite.Packet
	expires int64
	voucher string
}

func (f *selfServiceHTTPFixture) invitation(t *testing.T, ttl time.Duration) selfServiceHTTPInvite {
	t.Helper()
	id := opaqueID(t)
	return selfServiceHTTPInvite{
		id: id, proof: key(t),
		packet:  invite.Packet{Version: 1, InviteID: id, Enc: make([]byte, 32), Ciphertext: make([]byte, 32)},
		expires: f.now().Add(ttl).Unix(), voucher: strings.Repeat("v", 32) + opaqueID(t),
	}
}

func (i selfServiceHTTPInvite) input() map[string]any {
	return map[string]any{"proof_key": public(i.proof), "packet": i.packet, "expires_at": i.expires, "voucher": i.voucher}
}

func (i selfServiceHTTPInvite) claim(t *testing.T, reader ed25519.PrivateKey) invite.ClaimPacket {
	t.Helper()
	digest, err := invite.PacketDigest(i.packet)
	if err != nil {
		t.Fatal(err)
	}
	data, err := codec.Canonical(inviteProof{
		Version: 1, InviteID: i.id, ClaimID: opaqueID(t), ReaderKey: public(reader), CiphertextHash: digest, ExpiresAt: i.expires,
	})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := ptcrypto.Sign(data, i.id, ptcrypto.InviteProofType, i.proof)
	if err != nil {
		t.Fatal(err)
	}
	return invite.ClaimPacket{Packet: i.packet, Proof: proof}
}
