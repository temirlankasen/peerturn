package store

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func newStore(t *testing.T, limits Limits) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "relay.db"), limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if operationErr := s.Close(); operationErr != nil {
			t.Error(operationErr)
		}
	})
	return s
}

func seed(t *testing.T, s *Store, now time.Time) Message {
	t.Helper()
	ctx := context.Background()
	if operationErr := s.CreateMailbox(ctx, Mailbox{ID: "box", ReadKey: "reader"}, ""); operationErr != nil {
		t.Fatal(operationErr)
	}
	if operationErr := s.PutPermit(ctx, "box", "reader", Permit{ID: "permit", WriterKey: "writer", Epoch: 1, ExpiresAt: now.Add(time.Hour)}); operationErr != nil {
		t.Fatal(operationErr)
	}
	return Message{MailboxID: "box", PermitID: "permit", PermitEpoch: 1, WriterKey: "writer", ID: "message", Packet: []byte("opaque-ciphertext"), ExpiresAt: now.Add(time.Minute)}
}

func requireStatus(t *testing.T, err error, status int) {
	t.Helper()
	var target *Error
	if !errors.As(err, &target) || target.Status != status {
		t.Fatalf("want status %d, got %v", status, err)
	}
}

func TestDurableReplayAckAndExpiry(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	path := filepath.Join(t.TempDir(), "relay.db")
	s, err := Open(path, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	m := seed(t, s, now)
	epoch, err := s.BackupEpoch(ctx)
	if err != nil || epoch != 1 {
		t.Fatalf("initial backup epoch: %d, %v", epoch, err)
	}
	got, err := s.PutMessage(ctx, m, now)
	if err != nil || got.Seq != 1 || got.Duplicate {
		t.Fatalf("first receipt: %+v, %v", got, err)
	}
	requireStatus(t, s.Ack(ctx, "box", "reader", 1, now), 400)
	batch, err := s.Fetch(ctx, "box", "reader", 0, 100, now)
	if err != nil || len(batch) != 1 || string(batch[0].Packet) != string(m.Packet) {
		t.Fatalf("fetch: %+v, %v", batch, err)
	}
	if operationErr := s.Ack(ctx, "box", "reader", 1, now); operationErr != nil {
		t.Fatal(operationErr)
	}
	if operationErr := s.Close(); operationErr != nil {
		t.Fatal(operationErr)
	}
	s, err = Open(path, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if operationErr := s.Close(); operationErr != nil {
			t.Error(operationErr)
		}
	}()
	restoredEpoch, err := s.BackupEpoch(ctx)
	if err != nil || restoredEpoch != epoch {
		t.Fatalf("backup epoch changed after restart: %d, %v", restoredEpoch, err)
	}
	got, err = s.PutMessage(ctx, m, now.Add(2*time.Minute))
	if err != nil || got.Seq != 1 || !got.Duplicate {
		t.Fatalf("durable duplicate: %+v, %v", got, err)
	}
	m.Packet = []byte("changed-ciphertext")
	_, err = s.PutMessage(ctx, m, now.Add(2*time.Minute))
	requireStatus(t, err, 409)
}

func TestConcurrentSequenceAndClaim(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{})
	m := seed(t, s, now)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			copyMessage := m
			copyMessage.ID = strconv.Itoa(i)
			if _, operationErr := s.PutMessage(ctx, copyMessage, now); operationErr != nil {
				t.Error(operationErr)
			}
		})
	}
	wg.Wait()
	batch, err := s.Fetch(ctx, "box", "reader", 0, 100, now)
	if err != nil || len(batch) != 20 {
		t.Fatalf("fetch: %d, %v", len(batch), err)
	}
	for i, item := range batch {
		if item.Seq != int64(i+1) {
			t.Fatalf("noncontiguous sequence: %d", item.Seq)
		}
	}
	if operationErr := s.PutInvite(ctx, Invite{ID: "invite", ProofKey: "proof", Packet: []byte("opaque"), ExpiresAt: now.Add(time.Minute)}); operationErr != nil {
		t.Fatal(operationErr)
	}
	claims := make(chan error, 2)
	for _, id := range []string{"one", "two"} {
		wg.Go(func() {
			_, claimErr := s.ClaimInvite(ctx, "invite", Claim{ID: id, ReaderKey: id, Packet: []byte(id)}, now)
			claims <- claimErr
		})
	}
	wg.Wait()
	one, two := <-claims, <-claims
	if (one == nil) == (two == nil) {
		t.Fatalf("want exactly one claim, got %v / %v", one, two)
	}
	if one != nil {
		requireStatus(t, one, 410)
	} else {
		requireStatus(t, two, 410)
	}
}

func TestQuotaAndRevocation(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{MailboxItems: 1, MailboxBytes: 20})
	m := seed(t, s, now)
	if _, operationErr := s.PutMessage(ctx, m, now); operationErr != nil {
		t.Fatal(operationErr)
	}
	m.ID = "second"
	_, err := s.PutMessage(ctx, m, now)
	requireStatus(t, err, 429)
	if operationErr := s.RevokePermit(ctx, "box", "reader", "permit", 2); operationErr != nil {
		t.Fatal(operationErr)
	}
	_, err = s.PutMessage(ctx, m, now)
	requireStatus(t, err, 403)
	err = s.PutPermit(ctx, "box", "reader", Permit{ID: "permit", WriterKey: "writer", Epoch: 1, ExpiresAt: now.Add(time.Hour)})
	requireStatus(t, err, 409)
}
