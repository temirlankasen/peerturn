package store

import (
	"context"
	"testing"
	"time"
)

func TestRevokedInviteCannotBeRestoredByProvisionRetry(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{})
	invite := Invite{ID: "invite", ProofKey: "proof", Packet: []byte("ciphertext"), ExpiresAt: now.Add(time.Minute)}
	if err := s.PutInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeInvite(ctx, invite.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.PutInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	_, err := s.GetInvite(ctx, invite.ID, now)
	requireStatus(t, err, 410)
	_, err = s.ClaimInvite(ctx, invite.ID, Claim{ID: "claim", ReaderKey: "reader", Packet: []byte("claim-ciphertext")}, now)
	requireStatus(t, err, 410)
	if operationErr := s.RevokeInvite(ctx, "not-yet-provisioned"); operationErr != nil {
		t.Fatal(operationErr)
	}
	invite.ID = "not-yet-provisioned"
	requireStatus(t, s.PutInvite(ctx, invite), 409)
}
