package store

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestUnifiedNativeRevokeKeepsExactAttemptAndQueueEvidence(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	id := f.message.MessageID
	const attempt = "11111111111111111111111111111111"
	if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeDelivery(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	r := protocol.ReceiptPayload{MessageID: id, AttemptID: attempt, Stage: "adapter_accepted", EvidenceKind: "native_queue", At: testNow}
	const result = `{"item_ids":["queue-first","queue-second"]}`
	if err := s.DeliveryResult(t.Context(), attempt, r, []byte(result)); err != nil {
		t.Fatal(err)
	}
	queues, err := s.NativeQueues(t.Context(), "grant_one")
	if err != nil || len(queues) != 1 || len(queues[0].ItemIDs) != 2 || queues[0].ItemIDs[1] != "queue-second" {
		t.Fatalf("queue evidence: %+v %v", queues, err)
	}
	if err = s.NativeDequeued(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err = s.GrantRevokeLocal(t.Context(), "grant_one", 1, testNow+1); err != nil {
		t.Fatal(err)
	}
	if got, readErr := s.Receipt(t.Context(), id); readErr != nil || got.Stage != "revoked" || got.Outcome != "dequeued" || got.AttemptID != attempt {
		t.Fatalf("dequeue/revoke distinction: %+v %v", got, readErr)
	}
	if breach, startErr := s.NativePeerStarted(t.Context(), id, testNow+2); startErr != nil || !breach {
		t.Fatalf("late revoked start: %v %v", breach, startErr)
	}
	if got, readErr := s.Receipt(t.Context(), id); readErr != nil || got.Stage != "in_doubt" || got.Outcome != "revoked_started" || got.AttemptID != attempt || got.ReasonCode != "revoked" {
		t.Fatalf("revoked start erased exact attempt: %+v %v", got, readErr)
	}
	var sealed []byte
	if err = s.db.QueryRowContext(t.Context(), `SELECT native_result FROM delivery_attempts WHERE id=?`, attempt).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	plain, err := s.unseal("delivery_attempts", attempt, sealed)
	if err != nil || string(plain) != result {
		t.Fatalf("revoke erased original queue IDs: %q %v", plain, err)
	}
}
