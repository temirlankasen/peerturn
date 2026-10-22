package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestDiskLimitNeverIssuesUncommittedReceipt(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	path := filepath.Join(t.TempDir(), "relay.db")
	const budget = 1 << 20
	s, err := Open(path, Limits{GlobalBytes: budget, MailboxBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	m := seed(t, s, now)
	m.Packet = bytes.Repeat([]byte{0x9a}, 8<<10)
	var accepted int64
	failed := false
	for i := range 16 {
		m.ID = strconv.Itoa(i)
		receipt, writeErr := s.PutMessage(ctx, m, now)
		if writeErr != nil {
			requireStatus(t, writeErr, 429)
			if receipt.Seq != 0 {
				t.Fatal("storage failure exposed uncommitted receipt")
			}
			failed = true
			break
		}
		accepted++
	}
	if !failed || accepted == 0 {
		t.Fatalf("storage failure not exercised, accepted %d", accepted)
	}
	high, err := s.HighSeq(ctx, "box")
	if err != nil || high != accepted {
		t.Fatalf("failed write changed sequence: %d, %v", high, err)
	}
	var size int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, statErr := os.Stat(path + suffix)
		if statErr == nil {
			size += info.Size()
		} else if !os.IsNotExist(statErr) {
			t.Fatal(statErr)
		}
	}
	if size > budget {
		t.Fatalf("disk allocation exceeded budget: %d", size)
	}
	if operationErr := s.Close(); operationErr != nil {
		t.Fatal(operationErr)
	}
	s, err = Open(path, Limits{GlobalBytes: budget})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if operationErr := s.Close(); operationErr != nil {
			t.Error(operationErr)
		}
	}()
	high, err = s.HighSeq(ctx, "box")
	if err != nil || high != accepted {
		t.Fatalf("restart lost committed sequence: %d, %v", high, err)
	}
}

func TestByteCappedFetchAndIssuedCursor(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{})
	m := seed(t, s, now)
	m.Packet = bytes.Repeat([]byte{0xaf}, 180<<10)
	for i := range 8 {
		m.ID = strconv.Itoa(i)
		if _, operationErr := s.PutMessage(ctx, m, now); operationErr != nil {
			t.Fatal(operationErr)
		}
	}
	_, err := s.Fetch(ctx, "box", "reader", 5, 100, now)
	requireStatus(t, err, 400)
	batch, err := s.Fetch(ctx, "box", "reader", 0, 100, now)
	if err != nil || len(batch) != 5 {
		t.Fatalf("byte cap: %d items, %v", len(batch), err)
	}
	requireStatus(t, s.Ack(ctx, "box", "reader", 6, now), 400)
	if operationErr := s.Ack(ctx, "box", "reader", 5, now); operationErr != nil {
		t.Fatal(operationErr)
	}
	batch, err = s.Fetch(ctx, "box", "reader", 5, 100, now.Add(2*time.Minute))
	if err != nil || len(batch) != 3 {
		t.Fatalf("expiry fetch: %d, %v", len(batch), err)
	}
	for _, item := range batch {
		if !item.Tombstone || len(item.Packet) != 0 {
			t.Fatal("expired ciphertext returned instead of tombstone")
		}
	}
	if operationErr := s.Ack(ctx, "box", "reader", 8, now.Add(2*time.Minute)); operationErr != nil {
		t.Fatal(operationErr)
	}
}

func TestMetadataBackpressurePreservesReplay(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{MetadataItems: 3})
	m := seed(t, s, now)
	if _, operationErr := s.PutMessage(ctx, m, now); operationErr != nil {
		t.Fatal(operationErr)
	}
	m.ID = "other"
	_, err := s.PutMessage(ctx, m, now)
	requireStatus(t, err, 429)
	m.ID = "message"
	got, err := s.PutMessage(ctx, m, now)
	if err != nil || !got.Duplicate || got.Seq != 1 {
		t.Fatalf("metadata pressure lost replay receipt: %+v, %v", got, err)
	}
}

func TestReopenRejectsDatabaseAboveSmallerDiskBudget(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	path := filepath.Join(t.TempDir(), "relay.db")
	s, err := Open(path, Limits{GlobalBytes: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	m := seed(t, s, now)
	m.Packet = bytes.Repeat([]byte{0xbe}, 64<<10)
	for i := range 8 {
		if i > 0 {
			m.MailboxID, m.WriterKey = "box-"+strconv.Itoa(i), "writer-"+strconv.Itoa(i)
			readKey := "reader-" + strconv.Itoa(i)
			if createErr := s.CreateMailbox(ctx, Mailbox{ID: m.MailboxID, ReadKey: readKey}, ""); createErr != nil {
				t.Fatal(createErr)
			}
			if permitErr := s.PutPermit(ctx, m.MailboxID, readKey, Permit{ID: m.PermitID, WriterKey: m.WriterKey, Epoch: 1, ExpiresAt: now.Add(time.Hour)}); permitErr != nil {
				t.Fatal(permitErr)
			}
		}
		m.ID = strconv.Itoa(i)
		if _, operationErr := s.PutMessage(ctx, m, now); operationErr != nil {
			t.Fatal(operationErr)
		}
	}
	if operationErr := s.Close(); operationErr != nil {
		t.Fatal(operationErr)
	}
	s, err = Open(path, Limits{GlobalBytes: 1 << 20})
	if err == nil {
		if operationErr := s.Close(); operationErr != nil {
			t.Fatal(operationErr)
		}
		t.Fatal("database above the configured disk budget was reopened")
	}
	requireStatus(t, err, 503)
}
