package store

import (
	"bytes"
	"strconv"
	"testing"
	"time"
)

type selfServiceMetadataQuarter struct {
	f        *selfServiceFixture
	roots    []selfServiceRoot
	child    Mailbox
	message  Message
	ticket   Ticket
	ordinary Mailbox
}

func newSelfServiceMetadataQuarter(t *testing.T) selfServiceMetadataQuarter {
	t.Helper()
	f := newSelfServiceFixture(t, Limits{GlobalBytes: 1 << 30, MetadataItems: 1000})
	q := selfServiceMetadataQuarter{f: f, ordinary: Mailbox{ID: "ordinary", ReadKey: "ordinary-reader"}}
	if err := f.s.CreateMailbox(t.Context(), q.ordinary, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.PutPermit(t.Context(), q.ordinary.ID, q.ordinary.ReadKey, Permit{
		ID: "write", WriterKey: "ordinary-writer", Epoch: 1, ExpiresAt: f.now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	own, invite := f.create(t, selfServiceKey(0), "quarter-root-0", selfServicePolicy())
	q.roots = append(q.roots, selfServiceRoot{own, invite})
	q.child = selfServiceMailbox(t, selfServiceKey(200))
	f.claim(t, invite, q.child.ReadKey)
	if err := f.s.CreateMailbox(t.Context(), q.child, invite.Voucher); err != nil {
		t.Fatal(err)
	}
	if err := f.s.PutPermit(t.Context(), q.child.ID, q.child.ReadKey, Permit{
		ID: "write", WriterKey: "child-writer", Epoch: 1, ExpiresAt: f.now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	q.message = Message{MailboxID: q.child.ID, PermitID: "write", PermitEpoch: 1, WriterKey: "child-writer",
		ID: "retained-message", Packet: []byte("opaque-quarter-message"), ExpiresAt: f.now.Add(time.Hour)}
	if receipt, err := f.s.PutMessage(t.Context(), q.message, f.now); err != nil || receipt.Seq != 1 {
		t.Fatalf("seed descendant custody: %+v, %v", receipt, err)
	}
	q.ticket = Ticket{ID: "retained-ticket", MailboxID: q.child.ID, ReaderKey: q.child.ReadKey, ExpiresAt: f.now.Add(time.Minute)}
	if err := f.s.PutTicket(t.Context(), q.ticket, f.now); err != nil {
		t.Fatal(err)
	}
	// 82 registries + 82 invitations + 82 own mailboxes + the child's mailbox,
	// permit, message and ticket = 250. Ordinary custody contributes two more.
	for i := 1; i < 82; i++ {
		mailbox, next := f.create(t, selfServiceKey(byte(i)), "quarter-root-"+strconv.Itoa(i), selfServicePolicy())
		q.roots = append(q.roots, selfServiceRoot{mailbox, next})
	}
	if got := selfServiceRetainedRows(t, f.s); got != 252 {
		t.Fatalf("fixture must fill exactly the self-service metadata quarter: total=%d", got)
	}
	return q
}

func selfServiceRetainedRows(t *testing.T, s *Store) int {
	t.Helper()
	var rows int
	if err := s.db.QueryRowContext(t.Context(), `SELECT
	 (SELECT COUNT(*) FROM relay_inviters)+(SELECT COUNT(*) FROM invites)+
	 (SELECT COUNT(*) FROM mailboxes)+(SELECT COUNT(*) FROM permits)+
	 (SELECT COUNT(*) FROM messages)+(SELECT COUNT(*) FROM tickets)`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestSelfServiceMetadataQuarterStopsAdmissionBeforeOrdinaryCustody(t *testing.T) {
	q := newSelfServiceMetadataQuarter(t)
	f, root := q.f, q.roots[1]
	mailbox, err := f.s.CreateOwnInvite(t.Context(), root.mailbox.ReadKey, root.invite, selfServicePolicy())
	if err != nil || mailbox != root.mailbox {
		t.Fatalf("quarter pressure broke exact non-growing retry: %+v, %v", mailbox, err)
	}
	mailbox, err = f.s.CreateOwnInvite(t.Context(), root.mailbox.ReadKey, f.invite("quarter-over-existing"), selfServicePolicy())
	selfServiceRequireNoCreation(t, mailbox, err, 429)
	mailbox, err = f.s.CreateOwnInvite(t.Context(), selfServiceKey(90), f.invite("quarter-over-new"), selfServicePolicy())
	selfServiceRequireNoCreation(t, mailbox, err, 429)
	if got := selfServiceRetainedRows(t, f.s); got != 252 {
		t.Fatalf("rejected admission leaked retained rows: %d", got)
	}
	if revokeErr := f.s.RevokeOwnedInvite(t.Context(), q.roots[0].invite.ID, q.roots[0].mailbox.ReadKey); revokeErr != nil {
		t.Fatal(revokeErr)
	}
	if blockErr := f.s.BlockInviter(t.Context(), q.roots[2].mailbox.ReadKey); blockErr != nil {
		t.Fatal(blockErr)
	}
	f.reopen(t)
	mailbox, err = f.s.CreateOwnInvite(t.Context(), root.mailbox.ReadKey, f.invite("quarter-over-after-restart"), selfServicePolicy())
	selfServiceRequireNoCreation(t, mailbox, err, 429)
	ordinary := q.message
	ordinary.MailboxID, ordinary.WriterKey, ordinary.ID = q.ordinary.ID, "ordinary-writer", "ordinary-spare"
	if receipt, err := f.s.PutMessage(t.Context(), ordinary, f.now); err != nil || receipt.Seq != 1 {
		t.Fatalf("self-service exhausted ordinary message capacity: %+v, %v", receipt, err)
	}
	if err := f.s.PutPermit(t.Context(), q.ordinary.ID, q.ordinary.ReadKey, Permit{
		ID: "spare-permit", WriterKey: "spare-writer", Epoch: 1, ExpiresAt: f.now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("self-service exhausted ordinary permit capacity: %v", err)
	}
}

func TestSelfServiceMetadataQuarterIncludesDescendantsAndRetainsRecovery(t *testing.T) {
	q := newSelfServiceMetadataQuarter(t)
	f := q.f
	second := selfServiceMailbox(t, selfServiceKey(201))
	// A claim adds ciphertext without another row; the independent byte cap still applies.
	f.claim(t, q.roots[1].invite, second.ReadKey)
	for _, operation := range []string{"enrollment", "permit", "ticket", "message"} {
		t.Run(operation, func(t *testing.T) {
			before := selfServiceRetainedRows(t, f.s)
			var err error
			switch operation {
			case "enrollment":
				err = f.s.CreateMailbox(t.Context(), second, q.roots[1].invite.Voucher)
			case "permit":
				err = f.s.PutPermit(t.Context(), q.child.ID, q.child.ReadKey, Permit{
					ID: "over-quarter", WriterKey: "other-writer", Epoch: 1, ExpiresAt: f.now.Add(time.Hour),
				})
			case "ticket":
				ticket := q.ticket
				ticket.ID = "over-quarter"
				err = f.s.PutTicket(t.Context(), ticket, f.now)
			case "message":
				message := q.message
				message.ID = "over-quarter"
				var receipt Receipt
				receipt, err = f.s.PutMessage(t.Context(), message, f.now)
				if receipt != (Receipt{}) {
					t.Fatalf("rejected metadata growth exposed receipt: %+v, %v", receipt, err)
				}
			}
			requireStatus(t, err, 429)
			if got := selfServiceRetainedRows(t, f.s); got != before {
				t.Fatalf("quota failure leaked rows: %d -> %d", before, got)
			}
		})
	}
	if err := f.s.UseControlNonce(t.Context(), q.child.ReadKey, "quarter-recovery", f.now.Add(time.Minute), f.now); err != nil {
		t.Fatal(err)
	}
	batch, err := f.s.Fetch(t.Context(), q.child.ID, q.child.ReadKey, 0, 100, f.now)
	if err != nil || len(batch) != 1 || !bytes.Equal(batch[0].Packet, q.message.Packet) {
		t.Fatalf("metadata pressure broke Fetch: %+v, %v", batch, err)
	}
	if ackErr := f.s.Ack(t.Context(), q.child.ID, q.child.ReadKey, 1, f.now); ackErr != nil {
		t.Fatal(ackErr)
	}
	if permitErr := f.s.RevokePermit(t.Context(), q.child.ID, q.child.ReadKey, "write", 2); permitErr != nil {
		t.Fatal(permitErr)
	}
	if receipt, putErr := f.s.PutMessage(t.Context(), q.message, f.now); putErr != nil || !receipt.Duplicate || receipt.Seq != 1 {
		t.Fatalf("metadata pressure lost committed receipt: %+v, %v", receipt, putErr)
	}
	// ACK frees bytes, while its retained message row still spends metadata.
	mailbox, err := f.s.CreateOwnInvite(t.Context(), q.roots[1].mailbox.ReadKey, f.invite("after-ack-still-full"), selfServicePolicy())
	selfServiceRequireNoCreation(t, mailbox, err, 429)
	if err := f.s.DeleteMailbox(t.Context(), q.child.ID, q.child.ReadKey); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	if err := f.s.CreateMailbox(t.Context(), second, q.roots[1].invite.Voucher); err != nil {
		t.Fatalf("rejected enrollment spent voucher or leaked metadata: %v", err)
	}
}
