package store

import "testing"

// An ambiguous attempt on a revoked grant can no longer produce a peer turn,
// so it stays on the owner's record without pausing other peers' delivery.
func TestRevokedAmbiguityStaysRecordedButStopsFencing(t *testing.T) {
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
	if err := s.RecoverInDoubt(t.Context(), testNow+1); err != nil {
		t.Fatal(err)
	}
	if uncertain, err := s.UncertainDeliveries(t.Context()); err != nil || !uncertain {
		t.Fatalf("live ambiguity lost its fence: %v %v", uncertain, err)
	}
	if err := s.GrantRevoke(t.Context(), "grant_one", 1, testNow+2, f.receipt); err != nil {
		t.Fatal(err)
	}
	if uncertain, err := s.UncertainDeliveries(t.Context()); err != nil || uncertain {
		t.Fatalf("revoked ambiguity still pauses delivery: %v %v", uncertain, err)
	}
	ids, err := s.UncertainDeliveryIDs(t.Context())
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("revoked ambiguity vanished from the owner's record: %v %v", ids, err)
	}
	if r, err := s.Receipt(t.Context(), id); err != nil || r.Stage != "in_doubt" {
		t.Fatalf("revoke rewrote the ambiguous outcome: %+v %v", r, err)
	}
}
