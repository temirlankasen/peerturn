package relay

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/relay/store"
)

func opaqueID(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func TestInvitePreviewProofAtomicClaimAndEnrollment(t *testing.T) {
	f := newFixture(t)
	proofKey := key(t)
	id := opaqueID(t)
	expires := time.Now().Add(10 * time.Minute).Unix()
	p := invite.Packet{Version: 1, InviteID: id, Enc: make([]byte, 32), Ciphertext: make([]byte, 32)}
	data, err := codec.Canonical(p)
	if err != nil {
		t.Fatal(err)
	}
	voucher := "local-test-voucher-940a2198cc154a89aadbbf800e9d"
	if err = f.s.db.PutInvite(t.Context(), store.Invite{ID: id, ProofKey: public(proofKey), Packet: data, ExpiresAt: time.Unix(expires, 0), Voucher: voucher}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		f.request(t, "GET", "/i/"+id, nil, nil, 200)
	}
	before := f.request(t, "GET", "/v1/invites/"+id, nil, f.writer, 200)
	if bytes.Contains(before, []byte(`"claim_packet"`)) || bytes.Contains(before, []byte(voucher)) {
		t.Fatal("uncommitted claim or voucher exposed")
	}
	claim := invite.ClaimPacket{Packet: p, Proof: "bad"}
	f.request(t, "POST", "/v1/invites/"+id+"/claims", claim, f.reader, 401)
	digest, err := invite.PacketDigest(p)
	if err != nil {
		t.Fatal(err)
	}
	claimID := opaqueID(t)
	proof, err := codec.Canonical(inviteProof{Version: 1, InviteID: id, ClaimID: claimID, ReaderKey: public(f.reader), CiphertextHash: digest, ExpiresAt: expires})
	if err != nil {
		t.Fatal(err)
	}
	claim.Proof, err = ptcrypto.Sign(proof, id, ptcrypto.InviteProofType, proofKey)
	if err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", "/v1/invites/"+id+"/claims", claim, f.reader, 200)
	f.request(t, "POST", "/v1/invites/"+id+"/claims", claim, f.reader, 200)
	f.request(t, "POST", "/v1/invites/"+id+"/claims", claim, f.writer, 403)
	committed := f.request(t, "GET", "/v1/invites/"+id, nil, f.writer, 200)
	if bytes.Contains(committed, []byte(`"claim_packet"`)) {
		t.Fatal("unrelated signer observes committed claim")
	}
	committed = f.request(t, "GET", "/v1/invites/"+id, nil, proofKey, 200)
	var retained struct {
		ClaimPacket json.RawMessage `json:"claim_packet"`
	}
	if err = json.Unmarshal(committed, &retained); err != nil {
		t.Fatal(err)
	}
	canonical, err := codec.Canonical(claim)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(retained.ClaimPacket, canonical) || bytes.Contains(committed, []byte(voucher)) {
		t.Fatal("committed claim not retrievable intact, or voucher leaked")
	}
	// Only the winning routing key can spend the recipient-scoped enrollment voucher.
	f.request(t, "POST", "/v1/mailboxes", map[string]string{"id": "wrong", "read_key": public(f.writer), "voucher": voucher}, f.writer, 403)
	f.request(t, "POST", "/v1/mailboxes", map[string]string{"id": "joined", "read_key": public(f.reader), "voucher": voucher}, f.reader, 201)
	f.request(t, "POST", "/v1/mailboxes", map[string]string{"id": "extra", "read_key": public(f.reader), "voucher": voucher}, f.reader, 403)
	if err = f.s.db.RevokeInvite(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", "/v1/invites/"+id, nil, f.reader, 410)
	f.request(t, "POST", "/v1/invites/"+id+"/claims", claim, f.reader, 410)
}

func TestClaimWithoutReaderBindingIsRejected(t *testing.T) {
	f := newFixture(t)
	proofKey := key(t)
	id := opaqueID(t)
	expires := f.s.now().Add(10 * time.Minute)
	p := invite.Packet{Version: 1, InviteID: id, Enc: make([]byte, 32), Ciphertext: make([]byte, 32)}
	data, err := codec.Canonical(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.db.PutInvite(t.Context(), store.Invite{ID: id, ProofKey: public(proofKey), Packet: data, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	digest, err := invite.PacketDigest(p)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := codec.Canonical(map[string]any{"wire_version": 1, "invite_id": id, "claim_id": opaqueID(t), "ciphertext_hash": digest, "expires_at": expires.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	jws, err := ptcrypto.Sign(proof, id, ptcrypto.InviteProofType, proofKey)
	if err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", "/v1/invites/"+id+"/claims", invite.ClaimPacket{Packet: p, Proof: jws}, f.writer, 403)
}
