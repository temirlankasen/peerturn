package store

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestPermanentMetadataQuotaPreservesAuthenticatedOwnerRecovery(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{MetadataItems: 3})
	m := seed(t, s, now)
	if _, err := s.PutMessage(ctx, m, now); err != nil {
		t.Fatal(err)
	}
	m.ID = "over-quota"
	receipt, err := s.PutMessage(ctx, m, now)
	requireStatus(t, err, 429)
	if receipt.Seq != 0 {
		t.Fatal("metadata rejection issued a receipt")
	}
	if nonceErr := s.UseNonce(ctx, "reader", "fetch", now.Add(time.Minute), now); nonceErr != nil {
		t.Fatal(nonceErr)
	}
	batch, err := s.Fetch(ctx, "box", "reader", 0, 100, now)
	if err != nil || len(batch) != 1 || batch[0].Seq != 1 {
		t.Fatalf("owner could not read at permanent quota: %+v, %v", batch, err)
	}
	if nonceErr := s.UseNonce(ctx, "reader", "ack", now.Add(time.Minute), now); nonceErr != nil {
		t.Fatal(nonceErr)
	}
	if ackErr := s.Ack(ctx, "box", "reader", 1, now); ackErr != nil {
		t.Fatal(ackErr)
	}
	if nonceErr := s.UseNonce(ctx, "reader", "revoke", now.Add(time.Minute), now); nonceErr != nil {
		t.Fatal(nonceErr)
	}
	if revokeErr := s.RevokePermit(ctx, "box", "reader", "permit", 2); revokeErr != nil {
		t.Fatal(revokeErr)
	}
	requireStatus(t, s.CheckPermit(ctx, "box", "permit", "writer", 1, now), 403)
	high, err := s.HighSeq(ctx, "box")
	if err != nil || high != 1 {
		t.Fatalf("quota/recovery changed message sequence: %d, %v", high, err)
	}
}

func TestReplayQuotaIsBoundedAndIndependentOfPermanentMetadata(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{MetadataItems: 3})
	for i := range 3 {
		if err := s.UseNonce(ctx, "reader", strconv.Itoa(i), now.Add(time.Minute), now); err != nil {
			t.Fatal(err)
		}
	}
	requireStatus(t, s.UseNonce(ctx, "reader", "overflow", now.Add(time.Minute), now), 429)
	requireStatus(t, s.UseNonce(ctx, "reader", "0", now.Add(time.Minute), now), 409)
	m := seed(t, s, now)
	if _, err := s.PutMessage(ctx, m, now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nonces`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("replay growth exceeded its independent cap: %d, %v", count, err)
	}
	if err := s.UseNonce(ctx, "reader", "after-expiry", now.Add(3*time.Minute), now.Add(121*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nonces`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("expired replay entries were not reclaimed: %d, %v", count, err)
	}
}
