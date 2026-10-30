package store

import (
	"bytes"
	"strconv"
	"testing"
	"time"
)

type selfServiceDescendant struct {
	mailbox Mailbox
	invite  Invite
	message Message
}

func (f *selfServiceFixture) descendants(t *testing.T) (Mailbox, []selfServiceDescendant) {
	t.Helper()
	var own Mailbox
	children := make([]selfServiceDescendant, 3)
	for i := range children {
		id := strconv.Itoa(i)
		var invite Invite
		own, invite = f.create(t, selfServiceKey(0), "parent-"+id, selfServicePolicy())
		child := selfServiceMailbox(t, selfServiceKey(byte(10+i)))
		f.claim(t, invite, child.ReadKey)
		if err := f.s.CreateMailbox(t.Context(), child, invite.Voucher); err != nil {
			t.Fatal(err)
		}
		writer := "descendant-writer-" + id
		if err := f.s.PutPermit(t.Context(), child.ID, child.ReadKey, Permit{
			ID: "permit", WriterKey: writer, Epoch: 1, ExpiresAt: f.now.Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		children[i] = selfServiceDescendant{child, invite, Message{
			MailboxID: child.ID, PermitID: "permit", PermitEpoch: 1, WriterKey: writer,
			ID: "child-message-" + id, Packet: bytes.Repeat([]byte{0x98}, 64<<10), ExpiresAt: f.now.Add(time.Hour),
		}}
	}
	return own, children
}

func TestSelfServiceQuarterBudgetIncludesVoucherCustodyAndPreservesRecovery(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{GlobalBytes: 4 << 20, MailboxBytes: 4 << 20})
	_, children := f.descendants(t)
	// At 4 MiB disk custody is 960512 bytes; its self-service quarter is
	// 240128. Each child individually fits existing mailbox/writer/tenant caps.
	// Together the third 64 KiB packet crosses that quarter, well below global.
	for _, child := range children[:2] {
		if _, err := f.s.PutMessage(t.Context(), child.message, f.now); err != nil {
			t.Fatal(err)
		}
	}
	receipt, err := f.s.PutMessage(t.Context(), children[2].message, f.now)
	requireStatus(t, err, 429)
	if receipt != (Receipt{}) {
		t.Fatalf("quarter rejection exposed a receipt: %+v", receipt)
	}
	f.reopen(t)
	_, err = f.s.PutMessage(t.Context(), children[2].message, f.now)
	requireStatus(t, err, 429)
	high, err := f.s.HighSeq(t.Context(), children[2].mailbox.ID)
	if err != nil || high != 0 {
		t.Fatalf("quota failure advanced child cursor: %d, %v", high, err)
	}
	quiet := Mailbox{ID: "admin-quiet", ReadKey: "admin-reader"}
	if mailboxErr := f.s.CreateMailbox(t.Context(), quiet, ""); mailboxErr != nil {
		t.Fatal(mailboxErr)
	}
	if permitErr := f.s.PutPermit(t.Context(), quiet.ID, quiet.ReadKey, Permit{
		ID: "permit", WriterKey: "quiet-writer", Epoch: 1, ExpiresAt: f.now.Add(time.Hour),
	}); permitErr != nil {
		t.Fatal(permitErr)
	}
	quietMessage := children[2].message
	quietMessage.MailboxID, quietMessage.WriterKey, quietMessage.ID = quiet.ID, "quiet-writer", "quiet-message"
	if _, putErr := f.s.PutMessage(t.Context(), quietMessage, f.now); putErr != nil {
		t.Fatalf("self-service quarter exhausted ordinary custody: %v", putErr)
	}
	if nonceErr := f.s.UseControlNonce(t.Context(), selfServiceKey(0), "revoke", f.now.Add(time.Minute), f.now); nonceErr != nil {
		t.Fatal(nonceErr)
	}
	if revokeErr := f.s.RevokeOwnedInvite(t.Context(), children[0].invite.ID, selfServiceKey(0)); revokeErr != nil {
		t.Fatalf("quota prevented owned revoke: %v", revokeErr)
	}
	child := children[0]
	if nonceErr := f.s.UseControlNonce(t.Context(), child.mailbox.ReadKey, "fetch-ack", f.now.Add(time.Minute), f.now); nonceErr != nil {
		t.Fatal(nonceErr)
	}
	batch, err := f.s.Fetch(t.Context(), child.mailbox.ID, child.mailbox.ReadKey, 0, 100, f.now)
	if err != nil || len(batch) != 1 || !bytes.Equal(batch[0].Packet, child.message.Packet) {
		t.Fatalf("quarter pressure prevented fetch: %+v, %v", batch, err)
	}
	if ackErr := f.s.Ack(t.Context(), child.mailbox.ID, child.mailbox.ReadKey, 1, f.now); ackErr != nil {
		t.Fatalf("quarter pressure prevented ACK: %v", ackErr)
	}
	if permitErr := f.s.RevokePermit(t.Context(), child.mailbox.ID, child.mailbox.ReadKey, "permit", 2); permitErr != nil {
		t.Fatal(permitErr)
	}
	duplicate, err := f.s.PutMessage(t.Context(), child.message, f.now)
	if err != nil || !duplicate.Duplicate || duplicate.Seq != 1 {
		t.Fatalf("recovery lost an existing receipt: %+v, %v", duplicate, err)
	}
	receipt, err = f.s.PutMessage(t.Context(), children[2].message, f.now)
	if err != nil || receipt.Seq != 1 || receipt.Duplicate {
		t.Fatalf("ACK did not reclaim ciphertext charge: %+v, %v", receipt, err)
	}
}

func TestSelfServiceDescendantChargeSurvivesInviteExpiryRevokeAndRootMailboxDeletion(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{GlobalBytes: 4 << 20, MailboxBytes: 4 << 20})
	own, children := f.descendants(t)
	for _, child := range children[:2] {
		if _, err := f.s.PutMessage(t.Context(), child.message, f.now); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.s.RevokeOwnedInvite(t.Context(), children[0].invite.ID, own.ReadKey); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(61 * time.Second)
	if err := f.s.Sweep(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
	if err := f.s.DeleteMailbox(t.Context(), own.ID, own.ReadKey); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	_, err := f.s.PutMessage(t.Context(), children[2].message, f.now)
	requireStatus(t, err, 429)
	// Retained invite->enrolled_mailbox links must still classify the existing
	// children; clearing ciphertext or deleting the root cannot launder custody.
	batch, err := f.s.Fetch(t.Context(), children[0].mailbox.ID, children[0].mailbox.ReadKey, 0, 100, f.now)
	if err != nil || len(batch) != 1 || batch[0].Tombstone {
		t.Fatalf("root deletion lost unrelated descendant custody: %+v, %v", batch, err)
	}
	if ackErr := f.s.Ack(t.Context(), children[0].mailbox.ID, children[0].mailbox.ReadKey, 1, f.now); ackErr != nil {
		t.Fatal(ackErr)
	}
	if _, putErr := f.s.PutMessage(t.Context(), children[2].message, f.now); putErr != nil {
		t.Fatalf("descendant ACK could not recover quota: %v", putErr)
	}
}

func TestSelfServiceQuarterBudgetIncludesClaimCiphertextAndRollsBackRejectedClaim(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{GlobalBytes: 4 << 20})
	key := selfServiceKey(0)
	_, first := f.create(t, key, "claim-one", selfServicePolicy())
	_, second := f.create(t, key, "claim-two", selfServicePolicy())
	claim := Claim{ID: "first", ReaderKey: selfServiceKey(1), Packet: bytes.Repeat([]byte{0x9a}, 128<<10)}
	if _, err := f.s.ClaimInvite(t.Context(), first.ID, claim, f.now); err != nil {
		t.Fatal(err)
	}
	claim.ID, claim.ReaderKey = "rejected", selfServiceKey(2)
	_, err := f.s.ClaimInvite(t.Context(), second.ID, claim, f.now)
	requireStatus(t, err, 429)
	got, err := f.s.GetInvite(t.Context(), second.ID, f.now)
	if err != nil || len(got.ClaimPacket) != 0 || got.ClaimReader != "" {
		t.Fatalf("rejected claim consumed invitation: %+v, %v", got, err)
	}
	if revokeErr := f.s.RevokeOwnedInvite(t.Context(), first.ID, key); revokeErr != nil {
		t.Fatal(revokeErr)
	}
	claim.ID = "different-winner-after-recovery"
	if _, claimErr := f.s.ClaimInvite(t.Context(), second.ID, claim, f.now); claimErr != nil {
		t.Fatalf("failed claim leaked quota or winner: %v", claimErr)
	}
	f.reopen(t)
	got, err = f.s.GetInvite(t.Context(), second.ID, f.now)
	if err != nil || got.ClaimReader != claim.ReaderKey || !bytes.Equal(got.ClaimPacket, claim.Packet) {
		t.Fatalf("winning claim not durable: %+v, %v", got, err)
	}
}
