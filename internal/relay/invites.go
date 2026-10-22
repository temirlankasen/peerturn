package relay

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/relay/store"
)

func (s *Server) discovery(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	epoch, err := s.db.BackupEpoch(ctx)
	if err != nil {
		storageError(w, err)
		return
	}
	reply(w, 200, map[string]any{"wire_versions": []int{1}, "relay_key": s.relayKey, "backup_epoch": epoch, "limits": map[string]any{"ciphertext_bytes": s.cfg.Limits.CiphertextBytes, "batch_items": s.cfg.Limits.BatchItems, "mailbox_items": s.cfg.Limits.MailboxItems, "mailbox_bytes": s.cfg.Limits.MailboxBytes, "long_poll_seconds": s.cfg.Limits.LongPollSeconds}})
}

func (s *Server) getInvite(w http.ResponseWriter, r *http.Request, key string, _ []byte) {
	i, err := s.db.GetInvite(r.Context(), r.PathValue("invite"), s.now())
	if err != nil {
		storageError(w, err)
		return
	}
	response := map[string]any{"id": i.ID, "proof_key": i.ProofKey, "packet": jsonPacket(i.Packet), "expires_at": i.ExpiresAt.Unix()}
	if len(i.ClaimPacket) > 0 && (key == i.ProofKey || key == i.ClaimReader) {
		response["claim_packet"] = jsonPacket(i.ClaimPacket)
	}
	reply(w, 200, response)
}

type inviteProof struct {
	Version        int    `json:"wire_version"`
	InviteID       string `json:"invite_id"`
	ClaimID        string `json:"claim_id"`
	ReaderKey      string `json:"reader_key"`
	CiphertextHash string `json:"ciphertext_hash"`
	ExpiresAt      int64  `json:"expires_at"`
}

func (s *Server) claimInvite(w http.ResponseWriter, r *http.Request, key string, body []byte) {
	now := s.now()
	i, err := s.db.GetInvite(r.Context(), r.PathValue("invite"), now)
	if err != nil {
		storageError(w, err)
		return
	}
	claim, err := invite.DecodeClaimPacket(body)
	if err != nil {
		protocolError(w, err)
		return
	}
	proofKey, err := base64.RawURLEncoding.Strict().DecodeString(i.ProofKey)
	if err != nil {
		failure(w, 503, "storage_unavailable")
		return
	}
	verified, err := ptcrypto.Verify(claim.Proof, i.ID, ptcrypto.InviteProofType, ed25519.PublicKey(proofKey))
	if err != nil {
		failure(w, 401, "invalid_invite_proof")
		return
	}
	var proof inviteProof
	if !decode(w, verified, &proof) {
		return
	}
	// Captured claim bytes cannot enroll another routing key.
	if proof.ReaderKey != key {
		failure(w, 403, "claim_reader_mismatch")
		return
	}
	digest, err := invite.PacketDigest(claim.Packet)
	if err != nil {
		protocolError(w, err)
		return
	}
	id, err := base64.RawURLEncoding.Strict().DecodeString(proof.ClaimID)
	if proof.Version != 1 {
		failure(w, 426, "unsupported_version")
		return
	}
	if err != nil || len(id) != 16 || proof.InviteID != i.ID || claim.Packet.InviteID != i.ID || proof.CiphertextHash != digest || proof.ExpiresAt > i.ExpiresAt.Unix() {
		failure(w, 400, "invalid_claim")
		return
	}
	if proof.ExpiresAt <= now.Unix() {
		failure(w, 410, "expired_invite")
		return
	}
	canonical, err := codec.Canonical(claim)
	if err != nil {
		protocolError(w, err)
		return
	}
	result, err := s.db.ClaimInvite(r.Context(), i.ID, store.Claim{ID: proof.ClaimID, ReaderKey: key, Packet: canonical}, now)
	if err != nil {
		storageError(w, err)
		return
	}
	reply(w, 200, map[string]string{"claim_id": result.ID, "stage": "relay_stored"})
}
