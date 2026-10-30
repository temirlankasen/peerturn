package store

import (
	"bytes"
	"testing"
	"time"
)

func TestSelfServiceExistingRevokeUnknownCannotReserveAnInvite(t *testing.T) {
	// Twelve global slots reserve three slots for the self-service quarter.
	f := newSelfServiceFixture(t, Limits{MetadataItems: 12})
	ctx, key := t.Context(), selfServiceKey(0)
	invite := f.invite("not-reserved-by-public-delete")
	for _, admin := range []bool{false, true} {
		requireStatus(t, f.s.CheckInviteRevoker(ctx, invite.ID, key, admin), 410)
		requireStatus(t, f.s.RevokeExistingInvite(ctx, invite.ID, key, admin), 410)
	}
	// Preserve the established owner-only API's deliberately different refusal.
	requireStatus(t, f.s.CheckInviteOwner(ctx, invite.ID, key), 403)
	requireStatus(t, f.s.RevokeOwnedInvite(ctx, invite.ID, key), 403)
	f.reopen(t)
	var count int
	if err := f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM invites`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unknown public revoke allocated history: %d, %v", count, err)
	}
	// All three quarter slots must remain available for a real admission.
	f.create(t, selfServiceKey(1), invite.ID, selfServicePolicy())
}

func TestSelfServiceExistingRevokeRetainsAuthorityAndHistory(t *testing.T) {
	for _, actor := range []string{"author", "admin"} {
		for _, state := range []string{"live", "claimed", "expired"} {
			t.Run(actor+"/"+state, func(t *testing.T) {
				f := newSelfServiceFixture(t, Limits{})
				ctx, owner := t.Context(), selfServiceKey(0)
				_, invite := f.create(t, owner, "retained-authorship", selfServicePolicy())
				createdWant := f.now.UnixNano()
				claim := Claim{ID: "late-claim", ReaderKey: selfServiceKey(1), Packet: []byte("opaque-claim")}
				retainedClaimWant := ""
				if state != "live" {
					claim = f.claim(t, invite, selfServiceKey(1))
					retainedClaimWant = claim.ID
				}
				if state == "expired" {
					f.now = f.now.Add(2 * time.Minute)
				}
				if err := f.s.CheckInviteRevoker(ctx, invite.ID, owner, false); err != nil {
					t.Fatal(err)
				}
				for _, stranger := range []string{selfServiceKey(2), invite.ProofKey} {
					requireStatus(t, f.s.CheckInviteRevoker(ctx, invite.ID, stranger, false), 403)
					// A successful author preflight is not a lease for another signer.
					requireStatus(t, f.s.RevokeExistingInvite(ctx, invite.ID, stranger, false), 403)
				}
				key, admin := owner, actor == "admin"
				if admin {
					key = selfServiceKey(3)
				}
				for range 2 {
					if err := f.s.RevokeExistingInvite(ctx, invite.ID, key, admin); err != nil {
						t.Fatal(err)
					}
					f.reopen(t)
					if err := f.s.CheckInviteRevoker(ctx, invite.ID, key, admin); err != nil {
						t.Fatal(err)
					}
				}
				var retainedOwner, retainedClaim string
				var created int64
				var revoked bool
				var packet, claimPacket, voucherHash []byte
				err := f.s.db.QueryRowContext(ctx, `SELECT inviter_key,created_ns,revoked,packet,claim_packet,voucher_hash,COALESCE(claim_id,'')
				 FROM invites WHERE id=?`, invite.ID).Scan(&retainedOwner, &created, &revoked, &packet, &claimPacket, &voucherHash, &retainedClaim)
				if err != nil || retainedOwner != owner || created != createdWant || !revoked || len(packet)+len(claimPacket) != 0 ||
					!bytes.Equal(voucherHash, digest([]byte(invite.Voucher))) || retainedClaim != retainedClaimWant {
					t.Fatalf("public revoke changed retained history: owner=%q created=%d revoked=%v claim=%q, %v", retainedOwner, created, revoked, retainedClaim, err)
				}
				_, err = f.s.GetInvite(ctx, invite.ID, f.now)
				requireStatus(t, err, 410)
				_, err = f.s.ClaimInvite(ctx, invite.ID, claim, f.now)
				requireStatus(t, err, 410)
				requireStatus(t, f.s.CreateMailbox(ctx, selfServiceMailbox(t, claim.ReaderKey), invite.Voucher), 403)
				if err := f.s.CheckInviteOwner(ctx, invite.ID, owner); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestSelfServiceExistingRevokeLegacyRemainsAdminOnly(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	ctx, key := t.Context(), selfServiceKey(0)
	invite := f.invite("legacy-without-author")
	if err := f.s.PutInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	for _, signer := range []string{key, invite.ProofKey} {
		requireStatus(t, f.s.CheckInviteRevoker(ctx, invite.ID, signer, false), 403)
		requireStatus(t, f.s.RevokeExistingInvite(ctx, invite.ID, signer, false), 403)
	}
	f.requireDescriptor(t, invite)
	for range 2 {
		if err := f.s.RevokeExistingInvite(ctx, invite.ID, key, true); err != nil {
			t.Fatal(err)
		}
		f.reopen(t)
	}
	var legacy bool
	if err := f.s.db.QueryRowContext(ctx, `SELECT inviter_key IS NULL AND created_ns IS NULL FROM invites WHERE id=?`, invite.ID).Scan(&legacy); err != nil || !legacy {
		t.Fatalf("public revoke invented legacy ownership: %v, %v", legacy, err)
	}
	// The trusted provisioning API still intentionally preempts unknown IDs.
	if err := f.s.RevokeInvite(ctx, "operator-tombstone"); err != nil {
		t.Fatal(err)
	}
	invite.ID = "operator-tombstone"
	requireStatus(t, f.s.PutInvite(ctx, invite), 409)
}

func TestSelfServiceExistingRevokeRollbackPreservesCustodyAndAccounting(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	ctx, owner := t.Context(), selfServiceKey(0)
	_, invite := f.create(t, owner, "revoke-rollback", selfServicePolicy())
	claim := f.claim(t, invite, selfServiceKey(1))
	before := f.s.stats
	if _, err := f.s.db.ExecContext(ctx, `CREATE TRIGGER reject_public_revoke AFTER UPDATE OF revoked ON invites
	 BEGIN SELECT RAISE(ABORT,'synthetic revoke interruption'); END`); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, f.s.RevokeExistingInvite(ctx, invite.ID, owner, false), 503)
	f.requireDescriptor(t, invite)
	got, err := f.s.GetInvite(ctx, invite.ID, f.now)
	if err != nil || !bytes.Equal(got.ClaimPacket, claim.Packet) || f.s.stats != before {
		t.Fatalf("failed revoke lost custody or accounting: %+v, %v", got, err)
	}
	if _, err := f.s.db.ExecContext(ctx, `DROP TRIGGER reject_public_revoke`); err != nil {
		t.Fatal(err)
	}
	if err := f.s.RevokeExistingInvite(ctx, invite.ID, owner, false); err != nil {
		t.Fatal(err)
	}
	if got := before.bytes - f.s.stats.bytes; got != int64(len(invite.Packet)+len(claim.Packet)) || before.items != f.s.stats.items {
		t.Fatalf("public revoke release=%d, want packet+claim bytes and retained rows", got)
	}
}
