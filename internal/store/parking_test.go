package store

import (
	"bytes"
	"path/filepath"
	"sync"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestParkedCustodyRevalidatedWithoutRelayResubmit(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 2)
	commitOne(t, s, f, 1)
	results, err := s.RecheckParked(t.Context(), InboxRequest{Mailbox: "mailbox_b", GrantID: "grant_one", Open: f.options, Receipt: f.receipt})
	if err != nil || len(results) != 1 || results[0].Stage != "receiver_stored" {
		t.Fatalf("recheck: %+v %v", results, err)
	}
	item, err := s.Inbox(t.Context(), "00000000000000000000000000000002")
	if err != nil || item.State != "pending" || !bytes.Contains(item.Body, []byte("unique-store-plaintext-canary")) {
		t.Fatalf("promoted: %+v %v", item, err)
	}
}

func TestIndependentHandlesHaveOneCustodyWinner(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	var path, name string
	var seq int
	if err := s.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	other, err := Open(t.Context(), filepath.Clean(path), Options{Sealer: s.sealer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := other.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	r := InboxRequest{Packet: f.packet(t, 1), Mailbox: "mailbox_b", MailboxSeq: 1, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	var wg sync.WaitGroup
	winners := make(chan bool, 64)
	for i := range 64 {
		wg.Go(func() {
			handle := s
			if i%2 != 0 {
				handle = other
			}
			got, commitErr := handle.InboxCommit(t.Context(), r)
			if commitErr != nil {
				t.Error(commitErr)
				return
			}
			winners <- !got.Duplicate
		})
	}
	wg.Wait()
	close(winners)
	count := 0
	for winner := range winners {
		if winner {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("custody winners: %d", count)
	}
	// A mailbox sequence cannot be rebound to another ciphertext after custody.
	r.Packet = f.packet(t, 2)
	if _, err = s.InboxCommit(t.Context(), r); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("mailbox conflict: %v", err)
	}
}
