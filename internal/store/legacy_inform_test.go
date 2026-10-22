package store

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/protocol"
)

func legacyInformFixture(t *testing.T, s *Store, outcome string) protocol.ReceiptPayload {
	t.Helper()
	f := fixture(t, s)
	f.message.Kind = protocol.Inform
	commitOne(t, s, f, 1)
	r := protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: "11111111111111111111111111111111", Stage: "adapter_accepted", Outcome: outcome, EvidenceKind: "owner_boundary_offered", At: testNow}
	if outcome == "delivered" {
		r.EvidenceKind = "inbox_body_returned"
	}
	if err := s.RecordReceipt(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE inbox SET state=? WHERE id=?`, outcome, r.MessageID); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLegacyInformObservationSurvivesRevokeAndExpiry(t *testing.T) {
	for _, outcome := range []string{"offered", "delivered"} {
		t.Run(outcome, func(t *testing.T) {
			s := testStore(t)
			original := legacyInformFixture(t, s, outcome)
			if receipts, err := s.RevokeReceipts(t.Context(), "grant_one", testNow+1); err != nil || len(receipts) != 0 {
				t.Fatalf("legacy observation revoked: %+v %v", receipts, err)
			}
			if err := s.GrantRevokeLocal(t.Context(), "grant_one", 1, testNow+1); err != nil {
				t.Fatal(err)
			}
			if err := s.Expire(t.Context(), testNow+301); err != nil {
				t.Fatal(err)
			}
			if got, err := s.Receipt(t.Context(), original.MessageID); err != nil || got != original {
				t.Fatalf("legacy observation changed: %+v %v", got, err)
			}
		})
	}
}

func TestLegacyInformBodyReadPromotesExactOfferedAttempt(t *testing.T) {
	s := testStore(t)
	original := legacyInformFixture(t, s, "offered")
	r := protocol.ReceiptPayload{MessageID: original.MessageID, AttemptID: original.AttemptID, Stage: "delivered", Outcome: "delivered", EvidenceKind: "inbox_body_returned", At: testNow + 1}
	if err := s.InformResults(t.Context(), []InformResult{{Receipt: r}}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Receipt(t.Context(), original.MessageID); err != nil || got != r {
		t.Fatalf("legacy read did not promote exact attempt: %+v %v", got, err)
	}
}
