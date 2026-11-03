package store

import (
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestInboxReplyParentSurvivesLiveDelivery(t *testing.T) {
	for _, state := range []string{"authorized", "adapter_accepted"} {
		t.Run(state, func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			commitOne(t, s, f, 1)
			id, attempt := f.message.MessageID, strings.Repeat("1", 32)
			if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
				t.Fatal(err)
			}
			if err := s.AuthorizeDelivery(t.Context(), id, attempt, testNow); err != nil {
				t.Fatal(err)
			}
			if state == "adapter_accepted" {
				r := protocol.ReceiptPayload{MessageID: id, AttemptID: attempt, Stage: state, EvidenceKind: "synthetic_adapter", At: testNow}
				if err := s.DeliveryResult(t.Context(), attempt, r, []byte("accepted")); err != nil {
					t.Fatal(err)
				}
			}
			entry, err := s.ReplyParent(t.Context(), id)
			if err != nil || entry.MessageID != id || entry.ConversationID != f.message.ConversationID || entry.BudgetID != f.message.BudgetID || len(entry.Body) != 0 {
				t.Fatalf("delivered reply parent unavailable: state=%s err=%v", state, err)
			}
			pending, err := s.PendingRoute(t.Context(), envelope.Live, 10)
			if err != nil || len(pending) != 0 {
				t.Fatal("reply lookup reopened native delivery")
			}
			// A reply lookup never manufactures acceptance or bypasses expiry/revoke.
			if err = s.GrantRevoke(t.Context(), "grant_one", 1, testNow+1, f.receipt); err != nil {
				t.Fatal(err)
			}
			if _, err = s.ReplyParent(t.Context(), id); err == nil {
				t.Fatal("revoked parent retained reply authority")
			}
		})
	}
}

func TestInboxAcceptedReplyParentExpiry(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	id, attempt := f.message.MessageID, strings.Repeat("1", 32)
	if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeDelivery(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	r := protocol.ReceiptPayload{MessageID: id, AttemptID: attempt, Stage: "adapter_accepted", EvidenceKind: "synthetic_adapter", At: testNow}
	if err := s.DeliveryResult(t.Context(), attempt, r, []byte("accepted")); err != nil {
		t.Fatal(err)
	}
	s.clock = func() time.Time { return time.Unix(f.message.ExpiresAt, 0) }
	if _, err := s.ReplyParent(t.Context(), id); err == nil {
		t.Fatal("expired parent retained reply authority")
	}
}
