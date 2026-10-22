package store

import (
	"errors"
	"testing"

	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestReviewRevokeNeverClaimsOfferedInformWasUndelivered(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	f.message.Kind = protocol.Inform
	commitOne(t, s, f, 1)
	r := protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: f.message.MessageID, Stage: "offered", Outcome: "offered", EvidenceKind: "hook_notice", At: testNow}
	if err := s.InformResults(t.Context(), []InformResult{{Receipt: r}}); err != nil {
		t.Fatal(err)
	}
	receipts, err := s.RevokeReceipts(t.Context(), "grant_one", testNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range receipts {
		if receipt.Stage == "revoked" {
			t.Fatal("offered inform falsely reported revoked")
		}
	}
}

func TestReviewInformResultsAreAtomicAndCancelledWorkDoesNotOffer(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	f.message.Kind = protocol.Inform
	commitOne(t, s, f, 1)
	commitOne(t, s, f, 2)
	items, err := s.NativePending(t.Context(), 8)
	if err != nil {
		t.Fatal(err)
	}
	var results []InformResult
	for _, item := range items {
		results = append(results, InformResult{Receipt: protocol.ReceiptPayload{MessageID: item.MessageID, AttemptID: item.MessageID, Stage: "offered", Outcome: "offered", EvidenceKind: "hook_notice", At: testNow}})
	}
	injected := errors.New("atomic notice failure")
	s.abort = func(step string) error {
		if step == "before_commit" {
			return injected
		}
		return nil
	}
	if err = s.InformResults(t.Context(), results); !errors.Is(err, injected) {
		t.Fatalf("failure injection: %v", err)
	}
	s.abort = nil
	for _, item := range items {
		r, readErr := s.Receipt(t.Context(), item.MessageID)
		if readErr != nil || r.Stage != "receiver_stored" {
			t.Fatalf("partial offer: %+v %v", r, readErr)
		}
	}
	if err = s.InformResults(t.Context(), results); err != nil {
		t.Fatal(err)
	}
	if count, countErr := s.NativePendingCount(t.Context()); countErr != nil || count != 0 {
		t.Fatalf("pending count: %d %v", count, countErr)
	}
	page, err := s.ListInbox(t.Context(), 8)
	if err != nil || len(page) != 2 {
		t.Fatalf("offered bodies not readable: %v %v", page, err)
	}
}

func TestReviewPoisonedStoredItemDoesNotHideOtherPendingItems(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	commitOne(t, s, f, 2)
	if _, err := s.db.ExecContext(t.Context(), `UPDATE inbox SET packet=X'00' WHERE id=?`, f.message.MessageID); err != nil {
		t.Fatal(err)
	}
	items, err := s.NativePending(t.Context(), 8)
	if err != nil || len(items) != 2 || items[0].Error == nil || items[1].Error != nil {
		t.Fatalf("one poison blocked page: %+v %v", items, err)
	}
	if err = s.RejectNative(t.Context(), items[0].MessageID, "sealing_failed", testNow); err != nil {
		t.Fatal(err)
	}
	items, err = s.NativePending(t.Context(), 8)
	if err != nil || len(items) != 1 || items[0].Error != nil {
		t.Fatalf("poison did not terminate: %+v %v", items, err)
	}
}
