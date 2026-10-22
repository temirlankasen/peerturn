package store

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestNonceLanesReserveOwnerRecoveryAndNeverEvictLiveReplay(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{MetadataItems: 80})
	for i := range s.nonceLimits[2] {
		if err := s.UseAnonymousNonce(ctx, "anonymous-"+strconv.Itoa(i), "nonce", now.Add(time.Minute), now); err != nil {
			t.Fatal(err)
		}
	}
	requireStatus(t, s.UseAnonymousNonce(ctx, "overflow", "nonce", now.Add(time.Minute), now), 429)
	for i := range s.nonceLimits[0] {
		if err := s.UseNonce(ctx, "writer-"+strconv.Itoa(i), "nonce", now.Add(time.Minute), now); err != nil {
			t.Fatal(err)
		}
	}
	requireStatus(t, s.UseNonce(ctx, "overflow", "nonce", now.Add(time.Minute), now), 429)
	if err := s.UseControlNonce(ctx, "reader", "ack", now.Add(time.Minute), now); err != nil {
		t.Fatalf("other nonce lanes blocked owner recovery: %v", err)
	}
	for _, key := range []string{"anonymous-0", "writer-0", "reader"} {
		nonce := "nonce"
		if key == "reader" {
			nonce = "ack"
		}
		requireStatus(t, s.UseControlNonce(ctx, key, nonce, now.Add(time.Minute), now), 409)
	}
}

func TestNoncePerKeyBudgetDoesNotConsumeWholeRole(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{MetadataItems: 80})
	accepted := 0
	for i := range s.nonceLimits[0] {
		err := s.UseNonce(ctx, "writer", strconv.Itoa(i), now.Add(time.Minute), now)
		if err != nil {
			requireStatus(t, err, 429)
			break
		}
		accepted++
	}
	if accepted == 0 || accepted == s.nonceLimits[0] {
		t.Fatalf("one key exhausted a whole role: %d", accepted)
	}
	if err := s.UseNonce(ctx, "quiet-writer", "nonce", now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, s.UseNonce(ctx, "writer", "0", now.Add(time.Minute), now), 409)
}

func TestFailedAdmissionCannotUndoNonceExpiryPruning(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{})
	if err := s.UseNonce(ctx, "writer", "expired", now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, s.Ack(ctx, "missing", "stranger", 0, now.Add(121*time.Second)), 403)
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nonces`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected operation restored expired replay metadata: %d, %v", count, err)
	}
}
