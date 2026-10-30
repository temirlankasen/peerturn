package store

import (
	"strconv"
	"testing"
	"time"
)

type selfServiceRoot struct {
	mailbox Mailbox
	invite  Invite
}

func (f *selfServiceFixture) fillSelfServiceRows(t *testing.T) []selfServiceRoot {
	t.Helper()
	roots := make([]selfServiceRoot, 19)
	// 19 roots charge 57 retained 4096-byte rows plus small ciphertexts.
	// At 4 MiB the quarter is 240128: 58 rows fit, 59 cannot fit.
	for i := range roots {
		mailbox, invite := f.create(t, selfServiceKey(byte(i)), "root-"+strconv.Itoa(i), selfServicePolicy())
		roots[i] = selfServiceRoot{mailbox, invite}
	}
	return roots
}

func TestSelfServiceQuarterBudgetChargesRegistryOwnMailboxAndRetainedInviteRows(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{GlobalBytes: 4 << 20})
	roots := f.fillSelfServiceRows(t)
	mailbox, err := f.s.CreateOwnInvite(t.Context(), selfServiceKey(30), f.invite("twentieth"), selfServicePolicy())
	selfServiceRequireNoCreation(t, mailbox, err, 429)
	for _, root := range roots {
		if revokeErr := f.s.RevokeOwnedInvite(t.Context(), root.invite.ID, root.mailbox.ReadKey); revokeErr != nil {
			t.Fatal(revokeErr)
		}
	}
	f.reopen(t)
	mailbox, err = f.s.CreateOwnInvite(t.Context(), selfServiceKey(30), f.invite("after-tombstones"), selfServicePolicy())
	selfServiceRequireNoCreation(t, mailbox, err, 429)
	requireStatus(t, f.s.CheckReader(t.Context(), selfServiceMailbox(t, selfServiceKey(30)).ID, selfServiceKey(30)), 403)
	// A fresh key bypasses a per-key budget, but not the shared custody quarter.
	if err := f.s.CreateMailbox(t.Context(), Mailbox{ID: "admin-spare", ReadKey: "admin-key"}, ""); err != nil {
		t.Fatalf("self-service row quarter exhausted global custody: %v", err)
	}
}

func TestSelfServiceQuarterBudgetFencesVoucherEnrollmentPermitsAndTickets(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{GlobalBytes: 4 << 20})
	roots := f.fillSelfServiceRows(t)
	first, second := selfServiceMailbox(t, selfServiceKey(40)), selfServiceMailbox(t, selfServiceKey(41))
	f.claim(t, roots[0].invite, first.ReadKey)
	f.claim(t, roots[1].invite, second.ReadKey)
	if err := f.s.CreateMailbox(t.Context(), first, roots[0].invite.Voucher); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, f.s.CreateMailbox(t.Context(), second, roots[1].invite.Voucher), 429)
	requireStatus(t, f.s.CheckReader(t.Context(), second.ID, second.ReadKey), 403)
	requireStatus(t, f.s.PutPermit(t.Context(), first.ID, first.ReadKey, Permit{
		ID: "over-quarter", WriterKey: "writer", Epoch: 1, ExpiresAt: f.now.Add(time.Hour),
	}), 429)
	requireStatus(t, f.s.PutTicket(t.Context(), Ticket{
		ID: "over-quarter", MailboxID: first.ID, ReaderKey: first.ReadKey, ExpiresAt: f.now.Add(time.Minute),
	}, f.now), 429)
	_, err := f.s.GetPermit(t.Context(), first.ID, "over-quarter")
	requireStatus(t, err, 403)
	_, err = f.s.PeekTicket(t.Context(), "over-quarter", f.now)
	requireStatus(t, err, 403)
	f.reopen(t)
	requireStatus(t, f.s.CreateMailbox(t.Context(), second, roots[1].invite.Voucher), 429)
	if err := f.s.DeleteMailbox(t.Context(), roots[18].mailbox.ID, roots[18].mailbox.ReadKey); err != nil {
		t.Fatal(err)
	}
	if err := f.s.CreateMailbox(t.Context(), second, roots[1].invite.Voucher); err != nil {
		t.Fatalf("failed quota consumed enrollment voucher or leaked charge: %v", err)
	}
	if err := f.s.CheckReader(t.Context(), second.ID, second.ReadKey); err != nil {
		t.Fatal(err)
	}
}
