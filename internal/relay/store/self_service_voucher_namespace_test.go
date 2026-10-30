package store

import "testing"

func TestSelfServiceVoucherNamespaceCannotOccupyAnotherRoutingAddress(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	ctx := t.Context()
	_, invite := f.create(t, selfServiceKey(0), "self-issued-voucher", selfServicePolicy())
	secondKey, victimKey := selfServiceKey(1), selfServiceKey(2)
	f.claim(t, invite, secondKey)
	victim := selfServiceMailbox(t, victimKey)
	squatted := Mailbox{ID: victim.ID, ReadKey: secondKey}
	requireStatus(t, f.s.CreateMailbox(ctx, squatted, invite.Voucher), 403)
	requireStatus(t, f.s.CheckReader(ctx, victim.ID, secondKey), 403)
	requireStatus(t, f.s.CheckReader(ctx, victim.ID, victimKey), 403)
	var unspent bool
	if err := f.s.db.QueryRowContext(ctx, `SELECT enrolled_mailbox IS NULL FROM invites WHERE id=?`, invite.ID).Scan(&unspent); err != nil || !unspent {
		t.Fatalf("rejected namespace squat spent the voucher: %v, %v", unspent, err)
	}
	f.reopen(t)
	recipient := selfServiceMailbox(t, secondKey)
	for range 2 {
		if err := f.s.CreateMailbox(ctx, recipient, invite.Voucher); err != nil {
			t.Fatalf("correct derived recipient or exact enrollment retry failed: %v", err)
		}
	}
	if err := f.s.CheckReader(ctx, recipient.ID, secondKey); err != nil {
		t.Fatal(err)
	}
	// The second attacker's routing key must not permanently deny a fresh victim.
	got, _ := f.create(t, victimKey, "victim-can-publish", selfServicePolicy())
	if got != victim {
		t.Fatalf("victim lost its own address: %+v", got)
	}
}

func TestSelfServiceVoucherNamespaceKeepsTrustedLegacyCompatibility(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	ctx, readerKey := t.Context(), selfServiceKey(1)
	legacy := f.invite("operator-issued-legacy-voucher")
	if err := f.s.PutInvite(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	f.claim(t, legacy, readerKey)
	arbitrary := Mailbox{ID: "legacy-nonderived-address", ReadKey: readerKey}
	if err := f.s.CreateMailbox(ctx, arbitrary, legacy.Voucher); err != nil {
		t.Fatalf("trusted legacy voucher lost existing arbitrary-ID compatibility: %v", err)
	}
	if err := f.s.CreateMailbox(ctx, Mailbox{ID: "operator-nonderived-address", ReadKey: selfServiceKey(2)}, ""); err != nil {
		t.Fatalf("trusted local provisioning lost arbitrary-ID compatibility: %v", err)
	}
	f.reopen(t)
	if err := f.s.CreateMailbox(ctx, arbitrary, legacy.Voucher); err != nil {
		t.Fatal(err)
	}
	var legacyUnowned bool
	if err := f.s.db.QueryRowContext(ctx, `SELECT inviter_key IS NULL AND created_ns IS NULL AND enrolled_mailbox=?
	 FROM invites WHERE id=?`, arbitrary.ID, legacy.ID).Scan(&legacyUnowned); err != nil || !legacyUnowned {
		t.Fatalf("legacy enrollment invented self-service lineage: %v, %v", legacyUnowned, err)
	}
}
