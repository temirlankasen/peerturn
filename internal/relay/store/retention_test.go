package store

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestAckedDedupIsBoundedAndMailboxQuotaRecoversAfterGrace(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{MetadataItems: 40})
	s.SetClock(func() time.Time { return now })
	m := seed(t, s, now)
	if err := s.PutPermit(ctx, "box", "reader", Permit{ID: "permit", WriterKey: "writer", Epoch: 2, ExpiresAt: now.Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	m.PermitEpoch = 2
	var high int64
	for i := range 40 {
		m.ID = strconv.Itoa(i)
		receipt, err := s.PutMessage(ctx, m, now)
		if err != nil {
			requireStatus(t, err, 429)
			break
		}
		high = receipt.Seq
		if _, err := s.Fetch(ctx, "box", "reader", high-1, 100, now); err != nil {
			t.Fatal(err)
		}
		if err := s.Ack(ctx, "box", "reader", high, now); err != nil {
			t.Fatal(err)
		}
	}
	if high == 0 || high == 40 {
		t.Fatalf("retained dedup was not bounded: %d", high)
	}
	m.ID = "0"
	receipt, err := s.PutMessage(ctx, m, now.Add(2*time.Minute))
	if err != nil || !receipt.Duplicate || receipt.Seq != 1 {
		t.Fatalf("ACK lost receipt within grace: %+v, %v", receipt, err)
	}
	later := now.Add(time.Minute + DedupGrace + time.Second)
	m.ID, m.ExpiresAt = "new", later.Add(MinMessageTTL)
	if _, err := s.PutMessage(ctx, m, later); err != nil {
		t.Fatalf("ACKed dedup did not compact after bounded grace: %v", err)
	}
	var rows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("retention did not reclaim ACKed rows: %d, %v", rows, err)
	}
}

func TestMailboxBindingAndDeleteKeepUnrelatedCustody(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{})
	m := seed(t, s, now)
	for _, item := range []struct {
		mailbox, key string
		want         bool
	}{
		{"box", "reader", true}, {"box", "writer", true},
		{"other", "writer", false}, {"box", "stranger", false},
	} {
		got, err := s.KnownMailboxKey(ctx, item.mailbox, item.key, now)
		if err != nil || got != item.want {
			t.Fatalf("key binding %s/%s: %t, %v", item.mailbox, item.key, got, err)
		}
	}
	if _, err := s.PutMessage(ctx, m, now); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMailbox(ctx, Mailbox{ID: "other", ReadKey: "other-reader"}, ""); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, s.DeleteMailbox(ctx, "box", "writer"), 403)
	if err := s.RevokePermit(ctx, "box", "reader", "permit", 2); err != nil {
		t.Fatal(err)
	}
	known, err := s.KnownMailboxKey(ctx, "box", "writer", now)
	if err != nil || !known {
		t.Fatalf("retained writer receipt was inaccessible after revoke: %t, %v", known, err)
	}
	m.ID = "new-after-revoke"
	_, err = s.PutMessage(ctx, m, now)
	requireStatus(t, err, 403)
	if err := s.DeleteMailbox(ctx, "box", "reader"); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, s.CheckReader(ctx, "box", "reader"), 403)
	if err := s.CheckReader(ctx, "other", "other-reader"); err != nil {
		t.Fatal(err)
	}
	if s.stats.items != 1 || s.stats.bytes != 0 {
		t.Fatalf("delete left custody accounting: %+v", s.stats)
	}
}

func TestTicketPeekDoesNotConsumeAndTicketRowsAreBounded(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{})
	seed(t, s, now)
	ticket := Ticket{MailboxID: "box", ReaderKey: "reader", ExpiresAt: now.Add(time.Minute)}
	for i := range 8 {
		ticket.ID = strconv.Itoa(i)
		if err := s.PutTicket(ctx, ticket, now); err != nil {
			t.Fatal(err)
		}
	}
	ticket.ID = "overflow"
	requireStatus(t, s.PutTicket(ctx, ticket, now), 429)
	for range 2 {
		if _, err := s.PeekTicket(ctx, "0", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ConsumeTicket(ctx, "0", now); err != nil {
		t.Fatal(err)
	}
	_, err := s.PeekTicket(ctx, "0", now)
	requireStatus(t, err, 403)
}

func TestMinimumLifetimeHonorsWireSecondPrecision(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second).Add(900 * time.Millisecond)
	s := newStore(t, Limits{})
	m := seed(t, s, now)
	m.ExpiresAt = now.Add(MinMessageTTL).Truncate(time.Second)
	if _, err := s.PutMessage(ctx, m, now); err != nil {
		t.Fatalf("wire second precision rejected minimum TTL: %v", err)
	}
}

func TestSweepReclaimsIdleExpiryButRetainsUnackedTombstone(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{})
	m := seed(t, s, now)
	if _, err := s.PutMessage(ctx, m, now); err != nil {
		t.Fatal(err)
	}
	if err := s.UseControlNonce(ctx, "reader", "nonce", now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if err := s.Sweep(ctx, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var messages, nonces, packetBytes int
	if err := s.db.QueryRowContext(ctx, `SELECT
	 (SELECT COUNT(*) FROM messages),(SELECT COUNT(*) FROM nonces),
	 COALESCE((SELECT SUM(LENGTH(packet)) FROM messages),0)`).Scan(&messages, &nonces, &packetBytes); err != nil {
		t.Fatal(err)
	}
	if messages != 1 || nonces != 0 || packetBytes != 0 || s.stats.items != 3 || s.stats.bytes != 0 {
		t.Fatalf("idle expiry swept incorrectly: %d messages, %d nonces, %d bytes, %+v", messages, nonces, packetBytes, s.stats)
	}
}

func TestTenantAndWriterByteBudgetsLeaveRoomForQuietTenant(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{GlobalBytes: 4 << 20, MailboxBytes: 4 << 20})
	m := seed(t, s, now)
	m.Packet = make([]byte, 64<<10)
	for i := range 1 {
		m.ID = strconv.Itoa(i)
		if _, err := s.PutMessage(ctx, m, now); err != nil {
			t.Fatal(err)
		}
	}
	m.ID = "writer-overflow"
	_, writeErr := s.PutMessage(ctx, m, now)
	requireStatus(t, writeErr, 429)
	for i := range 2 {
		m.MailboxID, m.WriterKey = "alternate-"+strconv.Itoa(i), "alternate-writer-"+strconv.Itoa(i)
		if err := s.CreateMailbox(ctx, Mailbox{ID: m.MailboxID, ReadKey: "reader"}, ""); err != nil {
			t.Fatal(err)
		}
		if err := s.PutPermit(ctx, m.MailboxID, "reader", Permit{ID: m.PermitID, WriterKey: m.WriterKey, Epoch: 1, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.PutMessage(ctx, m, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateMailbox(ctx, Mailbox{ID: "tenant-overflow", ReadKey: "reader"}, ""); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, s.PutPermit(ctx, "tenant-overflow", "reader", Permit{ID: "permit", WriterKey: "overflow", Epoch: 1, ExpiresAt: now.Add(time.Hour)}), 429)
	if err := s.CreateMailbox(ctx, Mailbox{ID: "quiet", ReadKey: "quiet-reader"}, ""); err != nil {
		t.Fatal(err)
	}
	m.MailboxID, m.WriterKey = "quiet", "quiet-writer"
	if err := s.PutPermit(ctx, m.MailboxID, "quiet-reader", Permit{ID: m.PermitID, WriterKey: m.WriterKey, Epoch: 1, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutMessage(ctx, m, now); err != nil {
		t.Fatalf("noisy writer or tenant exhausted quiet tenant's byte capacity: %v", err)
	}
}
