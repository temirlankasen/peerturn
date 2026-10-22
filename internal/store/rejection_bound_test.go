package store

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/grant"
)

func TestRejectedPairsKeepBoundedSparseEvidence(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	g := grant.Grant{Version: 1, GrantID: "grant_inform", Issuer: f.recipient.cert.Binding, Subject: f.sender.cert.Binding, Scope: grant.Inform, GrantEpoch: 1, NotBefore: testNow - 5, ExpiresAt: testNow + 600, Limits: grant.DefaultLimits()}
	signed, err := grant.Issue(g, f.recipient.key)
	if err != nil {
		t.Fatal(err)
	}
	f.options.SignedGrant = signed
	f.message.GrantID, f.message.GrantHash = g.GrantID, grant.Hash(signed)
	if err = s.InstallGrant(t.Context(), f.options); err != nil {
		t.Fatal(err)
	}
	for slot := uint64(1); slot <= 100; slot++ {
		seq := slot + 1
		if slot%2 == 0 {
			seq = slot - 1
		}
		got, err := s.InboxCommit(t.Context(), InboxRequest{Packet: f.packet(t, seq), Mailbox: "mailbox_b", MailboxSeq: slot, GrantID: g.GrantID, Open: f.options, Receipt: f.receipt})
		if err != nil || got.Stage != "rejected" || got.Frontier != slot {
			t.Fatalf("paired scope refusal %d: %+v %v", seq, got, err)
		}
	}
	var sparse, used int
	if err := s.db.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM dedup WHERE channel IS NOT NULL),control_used FROM grants WHERE id=?`, g.GrantID).Scan(&sparse, &used); err != nil {
		t.Fatal(err)
	}
	if sparse != 0 || used != 5 {
		t.Fatalf("unbounded/unmetered refusal: sparse=%d used=%d", sparse, used)
	}
}

func TestRejectedPromotionDoesNotRemainPending(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 2)
	commitOne(t, s, f, 1)
	if _, err := s.db.ExecContext(t.Context(), `UPDATE grants SET burst=5,burst_start=? WHERE id='grant_one'`, testNow); err != nil {
		t.Fatal(err)
	}
	results, err := s.RecheckParked(t.Context(), InboxRequest{Mailbox: "mailbox_b", GrantID: "grant_one", Open: f.options, Receipt: f.receipt})
	if err != nil || len(results) != 1 || results[0].Stage != "rejected" {
		t.Fatalf("rejected promotion: %+v %v", results, err)
	}
	snapshot := gcSnapshot(t, s, f)
	if snapshot.NextSeq != 3 || len(snapshot.Pending) != 0 {
		t.Fatalf("rejected promotion leaked parked evidence: %+v", snapshot)
	}
}
