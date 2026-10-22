package store

import (
	"errors"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestResponderRejectionCommitsSignedNotificationAtomically(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	f.message.Kind = protocol.Ask
	commitOne(t, s, f, 1)
	r := protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: f.message.MessageID, Stage: "rejected",
		EvidenceKind: "responder_preflight", ReasonCode: "protected_path_policy_failed", Outcome: "responder_unavailable", At: testNow}
	notice := nativeNoticeFixture(t, s, f, r)
	packet, err := protocol.DecodePacket(notice.Packet)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("crash before responder rejection commit")
	s.abort = func(step string) error {
		if step == "before_commit" {
			return injected
		}
		return nil
	}
	if _, err = s.RejectPendingDelivery(t.Context(), r.MessageID, r.ReasonCode, testNow, notice); !errors.Is(err, injected) {
		t.Fatal("failure injection did not abort", err)
	}
	s.abort = nil
	if exists, readErr := s.OutboxExists(t.Context(), packet.Header.MessageID); readErr != nil || exists {
		t.Fatal("rejection notification escaped rollback", readErr)
	}
	before, err := s.Receipt(t.Context(), r.MessageID)
	if err != nil || before.Stage != "receiver_stored" {
		t.Fatal("partial local rejection", before, err)
	}
	got, err := s.RejectPendingDelivery(t.Context(), r.MessageID, r.ReasonCode, testNow, notice)
	if err != nil || got != r {
		t.Fatal("rejection not committed", got, err)
	}
	if exists, readErr := s.OutboxExists(t.Context(), packet.Header.MessageID); readErr != nil || !exists {
		t.Fatal("signed rejection missing", readErr)
	}
}

func TestResponderRejectionCannotSettleLiveOrAuthorizedInput(t *testing.T) {
	for _, kind := range []protocol.Kind{protocol.Coauthor, protocol.Ask} {
		t.Run(string(kind), func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			f.message.Kind = kind
			commitOne(t, s, f, 1)
			want := fault.ScopeDenied
			if kind == protocol.Ask {
				attempt := f.message.MessageID
				if _, err := s.DeliveryIntent(t.Context(), attempt, attempt, testNow); err != nil {
					t.Fatal(err)
				}
				if err := s.AuthorizeDelivery(t.Context(), attempt, attempt, testNow); err != nil {
					t.Fatal(err)
				}
				want = InvalidState
			}
			if _, err := s.RejectPendingDelivery(t.Context(), f.message.MessageID, "protected_path_policy_failed", testNow); !fault.IsCode(err, want) {
				t.Fatal("unavailable rejection changed live/authorized input", err)
			}
		})
	}
}
