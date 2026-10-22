package store

import (
	"errors"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestReleaseDeliveryOnlyBeforeNativeResult(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	id, attempt := f.message.MessageID, "11111111111111111111111111111111"
	if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	r := protocol.ReceiptPayload{MessageID: id, AttemptID: attempt, Stage: "adapter_accepted", EvidenceKind: "native_queue", At: testNow}
	if err := s.DeliveryResult(t.Context(), attempt, r, []byte("queue")); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("unauthorized acceptance: %v", err)
	}
	if err := s.AuthorizeDelivery(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if authorized, err := s.IsDeliveryAuthorized(t.Context(), id, attempt); err != nil || !authorized {
		t.Fatalf("missing authorization: %v %v", authorized, err)
	}
	for range 2 {
		if err := s.ReleaseDelivery(t.Context(), id, attempt, testNow); err != nil {
			t.Fatal(err)
		}
	}
	if authorized, err := s.IsDeliveryAuthorized(t.Context(), id, attempt); err != nil || authorized {
		t.Fatalf("release kept authorization: %v %v", authorized, err)
	}
	if err := s.AuthorizeDelivery(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.DeliveryResult(t.Context(), attempt, r, []byte("queue")); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseDelivery(t.Context(), id, attempt, testNow+1); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("release erased native acceptance: %v", err)
	}
}

func TestResolveDeliverySettlesExactAmbiguousAttempt(t *testing.T) {
	for _, sent := range []bool{false, true} {
		t.Run(map[bool]string{false: "not_sent", true: "sent"}[sent], func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			commitOne(t, s, f, 1)
			id, attempt := f.message.MessageID, "11111111111111111111111111111111"
			if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ResolveDelivery(t.Context(), id, sent, testNow); !fault.IsCode(err, fault.Conflict) {
				t.Fatalf("resolved non-ambiguous intent: %v", err)
			}
			if err := s.AuthorizeDelivery(t.Context(), id, attempt, testNow); err != nil {
				t.Fatal(err)
			}
			if err := s.RecoverInDoubt(t.Context(), testNow+1); err != nil {
				t.Fatal(err)
			}
			r, err := s.ResolveDelivery(t.Context(), id, sent, testNow+2)
			if err != nil || r.AttemptID != attempt || r.EvidenceKind != "native_reconciliation" {
				t.Fatalf("resolve: %+v %v", r, err)
			}
			if uncertain, checkErr := s.UncertainDeliveries(t.Context()); checkErr != nil || uncertain {
				t.Fatalf("reconciled attempt still fenced: %v %v", uncertain, checkErr)
			}
			wantStage, wantInbox := "receiver_stored", "pending"
			if sent {
				wantStage, wantInbox = "adapter_accepted", "adapter_accepted"
			}
			if got, receiptErr := s.Receipt(t.Context(), id); receiptErr != nil || got.Stage != wantStage || got.AttemptID != attempt {
				t.Fatalf("resolve receipt: %+v %v", got, receiptErr)
			}
			if got, inboxErr := s.Inbox(t.Context(), id); inboxErr != nil || got.State != wantInbox {
				t.Fatalf("resolve inbox: %+v %v", got, inboxErr)
			}
			if _, err = s.ResolveDelivery(t.Context(), id, !sent, testNow+3); !fault.IsCode(err, fault.Conflict) {
				t.Fatalf("second assertion replaced resolution: %v", err)
			}
			if !sent {
				r.Stage, r.EvidenceKind, r.At = "adapter_accepted", "native_queue", testNow+3
				if err = s.RecordReceipt(t.Context(), r); !fault.IsCode(err, fault.Conflict) {
					t.Fatalf("old unsent attempt gained effects: %v", err)
				}
				fresh, freshErr := s.DeliveryIntent(t.Context(), id, "22222222222222222222222222222222", testNow+3)
				if freshErr != nil || !fresh.Start {
					t.Fatalf("resolved unsent not reeligible: %+v %v", fresh, freshErr)
				}
			}
		})
	}
}

func TestResolveDeliveryPreservesRevocationQuarantine(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	id, attempt := f.message.MessageID, "11111111111111111111111111111111"
	if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeDelivery(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantRevoke(t.Context(), "grant_one", 1, testNow+1, f.receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveDelivery(t.Context(), id, false, testNow+2); err != nil {
		t.Fatal(err)
	}
	if item, err := s.Inbox(t.Context(), id); err != nil || item.State != "quarantined" {
		t.Fatalf("resolution removed quarantine: %+v %v", item, err)
	}
	if _, err := s.DeliveryIntent(t.Context(), id, "22222222222222222222222222222222", testNow+3); !fault.IsCode(err, fault.Revoked) {
		t.Fatalf("resolution restored revoked authority: %v", err)
	}
}

func TestAuthorizeFailureRollsBackDurableFence(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	id, attempt := f.message.MessageID, "11111111111111111111111111111111"
	if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("authorization commit failed")
	s.abort = func(step string) error {
		if step == "before_commit" {
			return injected
		}
		return nil
	}
	if err := s.AuthorizeDelivery(t.Context(), id, attempt, testNow); !errors.Is(err, injected) {
		t.Fatalf("authorization did not fail: %v", err)
	}
	s.abort = nil
	if err := s.RecoverInDoubt(t.Context(), testNow+1); err != nil {
		t.Fatal(err)
	}
	if uncertain, err := s.UncertainDeliveries(t.Context()); err != nil || uncertain {
		t.Fatalf("failed authorization fenced adapter: %v %v", uncertain, err)
	}
}
