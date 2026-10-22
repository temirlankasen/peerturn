package store

import (
	"bytes"
	"context"
	"strconv"
	"testing"
	"time"
)

func TestFullCustodyReturnsQuotaAndOwnerCanRecover(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{GlobalBytes: 4 << 20, MailboxBytes: 4 << 20})
	m := seed(t, s, now)
	m.Packet = bytes.Repeat([]byte{0x92}, 64<<10)
	var accepted int64
	for i := range 100 {
		m.ID = strconv.Itoa(i)
		_, err := s.PutMessage(ctx, m, now)
		if err != nil {
			requireStatus(t, err, 429)
			break
		}
		accepted++
	}
	if accepted == 0 || accepted == 100 {
		t.Fatalf("custody quota not exercised: %d", accepted)
	}
	if err := s.UseControlNonce(ctx, "reader", "recover", now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fetch(ctx, "box", "reader", 0, 100, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(ctx, "box", "reader", accepted, now); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokePermit(ctx, "box", "reader", "permit", 2); err != nil {
		t.Fatal(err)
	}
}

func TestGlobalCustodyBudgetAcrossTenantsReservesOwnerNonces(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{GlobalBytes: 4 << 20, MailboxBytes: 4 << 20})
	s.SetClock(func() time.Time { return now })
	packet := bytes.Repeat([]byte{0x9c}, 64<<10)
	accepted := 0
	for i := range 100 {
		id := strconv.Itoa(i)
		box, key, writer := "box-"+id, "reader-"+id, "writer-"+id
		if err := s.CreateMailbox(ctx, Mailbox{ID: box, ReadKey: key}, ""); err != nil {
			requireStatus(t, err, 429)
			break
		}
		if err := s.PutPermit(ctx, box, key, Permit{ID: "permit", WriterKey: writer, Epoch: 1, ExpiresAt: now.Add(time.Hour)}); err != nil {
			requireStatus(t, err, 429)
			break
		}
		m := Message{MailboxID: box, PermitID: "permit", PermitEpoch: 1, WriterKey: writer, ID: id, Packet: packet, ExpiresAt: now.Add(time.Hour)}
		if _, err := s.PutMessage(ctx, m, now); err != nil {
			requireStatus(t, err, 429)
			break
		}
		accepted++
	}
	if accepted < 2 || accepted == 100 || s.stats.items*rowCharge+s.stats.bytes < s.dataBudget-int64(len(packet))-rowCharge {
		t.Fatalf("global physical custody admission was not filled: %d, %+v, budget %d", accepted, s.stats, s.dataBudget)
	}
	for i := range s.nonceLimits[0] {
		if err := s.UseNonce(ctx, "writer-"+strconv.Itoa(i), "fill", now.Add(time.Minute), now); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UseControlNonce(ctx, "reader-0", "ack", now.Add(time.Minute), now); err != nil {
		t.Fatalf("full custody and normal nonce lane blocked owner: %v", err)
	}
	if _, err := s.Fetch(ctx, "box-0", "reader-0", 0, 100, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(ctx, "box-0", "reader-0", 1, now); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokePermit(ctx, "box-0", "reader-0", "permit", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMailbox(ctx, "box-0", "reader-0"); err != nil {
		t.Fatal(err)
	}
}

func TestExpiryDoesNotReleaseUnackedItemBudget(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{MailboxItems: 1, MetadataItems: 40})
	m := seed(t, s, now)
	if _, err := s.PutMessage(ctx, m, now); err != nil {
		t.Fatal(err)
	}
	m.ID, m.ExpiresAt = "second", now.Add(3*time.Minute)
	_, err := s.PutMessage(ctx, m, now.Add(2*time.Minute))
	requireStatus(t, err, 429)
	if err := s.CreateMailbox(ctx, Mailbox{ID: "quiet", ReadKey: "quiet-reader"}, ""); err != nil {
		t.Fatalf("noisy writer exhausted unrelated enrollment: %v", err)
	}
}

func TestMessageMinimumLifetime(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{})
	m := seed(t, s, now)
	m.ExpiresAt = now.Add(time.Second)
	_, err := s.PutMessage(ctx, m, now)
	requireStatus(t, err, 400)
}

func TestRepeatedFetchDoesNotWrite(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{})
	m := seed(t, s, now)
	if _, err := s.PutMessage(ctx, m, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fetch(ctx, "box", "reader", 0, 100, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	batch, err := s.Fetch(ctx, "box", "reader", 0, 100, now)
	if err != nil || len(batch) != 1 {
		t.Fatalf("unchanged issued cursor required a write: %d, %v", len(batch), err)
	}
}

func TestWriterCannotWatchMailboxTraffic(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{})
	seed(t, s, now)
	ticket := Ticket{ID: "writer-ticket", MailboxID: "box", WriterKey: "writer", PermitID: "permit", PermitEpoch: 1, ExpiresAt: now.Add(time.Minute)}
	requireStatus(t, s.CheckStream(ctx, ticket, now), 403)
	requireStatus(t, s.PutTicket(ctx, ticket, now), 403)
}

func TestExactReceiptSurvivesPermitRevoke(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{})
	m := seed(t, s, now)
	if _, err := s.PutMessage(ctx, m, now); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokePermit(ctx, "box", "reader", "permit", 2); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.PutMessage(ctx, m, now)
	if err != nil || receipt.Seq != 1 || !receipt.Duplicate {
		t.Fatalf("exact committed retry lost receipt after revoke: %+v, %v", receipt, err)
	}
	m.Packet = []byte("different")
	_, err = s.PutMessage(ctx, m, now)
	requireStatus(t, err, 409)
}

func TestOneOwnerCannotBurnGlobalPermitMetadata(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{MetadataItems: 40})
	seed(t, s, now)
	for i := range 80 {
		err := s.RevokePermit(ctx, "box", "reader", "revoked-"+strconv.Itoa(i), 2)
		if err != nil {
			requireStatus(t, err, 429)
			break
		}
	}
	if err := s.CreateMailbox(ctx, Mailbox{ID: "quiet", ReadKey: "quiet-reader"}, ""); err != nil {
		t.Fatalf("one owner's permit tombstones exhausted all metadata: %v", err)
	}
}

func TestSmallDiskDefaultMetadataIsolatesTinyMessageWriter(t *testing.T) {
	for _, budget := range []int64{1 << 20, 4 << 20} {
		t.Run(strconv.FormatInt(budget, 10), func(t *testing.T) {
			testSmallDiskTinyMessageWriter(t, budget)
		})
	}
}

func testSmallDiskTinyMessageWriter(t *testing.T, budget int64) {
	t.Helper()
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{GlobalBytes: budget})
	m := seed(t, s, now)
	m.Packet = []byte{0x7c}
	accepted := 0
	for i := range 1000 {
		m.ID = strconv.Itoa(i)
		_, err := s.PutMessage(ctx, m, now)
		if err != nil {
			requireStatus(t, err, 429)
			break
		}
		accepted++
	}
	if accepted == 0 || accepted == 1000 {
		t.Fatalf("writer metadata quota not exercised: %d", accepted)
	}
	if err := s.CreateMailbox(ctx, Mailbox{ID: "quiet", ReadKey: "quiet-reader"}, ""); err != nil {
		t.Fatalf("tiny-message writer exhausted unrelated enrollment on small disk after %d messages: %v", accepted, err)
	}
	if err := s.PutPermit(ctx, "quiet", "quiet-reader", Permit{ID: "quiet-permit", WriterKey: "quiet-writer", Epoch: 1, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	m.MailboxID, m.PermitID, m.WriterKey = "quiet", "quiet-permit", "quiet-writer"
	if _, err := s.PutMessage(ctx, m, now); err != nil {
		t.Fatalf("tiny-message writer exhausted unrelated custody: %v", err)
	}
}

func TestSmallDiskDefaultMetadataIsolatesTenantEnrollment(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, Limits{GlobalBytes: 4 << 20})
	for i := range 1000 {
		err := s.CreateMailbox(ctx, Mailbox{ID: "noisy-" + strconv.Itoa(i), ReadKey: "noisy-reader"}, "")
		if err != nil {
			requireStatus(t, err, 429)
			break
		}
	}
	if err := s.CreateMailbox(ctx, Mailbox{ID: "quiet", ReadKey: "quiet-reader"}, ""); err != nil {
		t.Fatalf("one tenant exhausted physical metadata through enrollment: %v", err)
	}
}

func TestEffectiveMetadataCapacityPreservesSmallConfiguredLimits(t *testing.T) {
	for _, capacity := range []int{1, 2, 3} {
		t.Run(strconv.Itoa(capacity), func(t *testing.T) {
			ctx, now := context.Background(), time.Now().UTC()
			s := newStore(t, Limits{GlobalBytes: 4 << 20, MetadataItems: capacity})
			if err := s.CreateMailbox(ctx, Mailbox{ID: "box", ReadKey: "reader"}, ""); err != nil {
				t.Fatal(err)
			}
			permit := Permit{ID: "permit", WriterKey: "writer", Epoch: 1, ExpiresAt: now.Add(time.Hour)}
			if capacity == 1 {
				requireStatus(t, s.PutPermit(ctx, "box", "reader", permit), 429)
				return
			}
			if err := s.PutPermit(ctx, "box", "reader", permit); err != nil {
				t.Fatal(err)
			}
			m := Message{MailboxID: "box", PermitID: "permit", PermitEpoch: 1, WriterKey: "writer", ID: "message", Packet: []byte{1}, ExpiresAt: now.Add(time.Minute)}
			_, err := s.PutMessage(ctx, m, now)
			if capacity == 2 {
				requireStatus(t, err, 429)
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOneMiBPacketCannotConsumeGlobalCustodyThroughMinimumAllowance(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{GlobalBytes: 1 << 20})
	m := seed(t, s, now)
	m.Packet = make([]byte, s.dataBudget-3*rowCharge)
	_, err := s.PutMessage(ctx, m, now)
	requireStatus(t, err, 429)
	if err := s.CreateMailbox(ctx, Mailbox{ID: "quiet", ReadKey: "quiet-reader"}, ""); err != nil {
		t.Fatalf("one packet consumed every tenant's custody budget: %v", err)
	}
}
