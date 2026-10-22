package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func askEvidence(r AskReservation, stage string) protocol.ReceiptPayload {
	return protocol.ReceiptPayload{MessageID: r.MessageID, AttemptID: r.AttemptID,
		Stage: stage, EvidenceKind: "supervisor_event", At: r.Now}
}

func redactedAsk(t *testing.T) (*Store, *testFixture, AskReservation) {
	t.Helper()
	s, f, r := askFixture(t)
	reserveAsk(t, s, r)
	if err := s.AuthorizeAsk(t.Context(), r.RunID, r.Now); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"responder_started", "model_input", "turn_completed", "reply_redacted"} {
		if err := s.AdvanceAsk(t.Context(), r.RunID, askEvidence(r, stage), []byte("private-child-native-id")); err != nil {
			t.Fatal(err)
		}
	}
	return s, f, r
}

func askReplyRequest(t *testing.T, s *Store, f *testFixture) OutboxRequest {
	t.Helper()
	installReturnGrant(t, s, f)
	m := f.message
	m.MessageID, m.FromSession, m.ToSession = strings.Repeat("c", 32), "recipient", "sender"
	m.GrantID, m.GrantHash = "grant_reverse", grant.Hash(f.reverse)
	m.Kind, m.ParentID, m.ReplyTo, m.Hop = protocol.Reply, f.message.MessageID, f.message.MessageID, 1
	m.BodyUTF8 = "complete redacted answer"
	h := f.header
	h.MessageID, h.DstMailbox, h.RoutePermitID = m.MessageID, "mailbox_a", "permit_b"
	return OutboxRequest{Packet: sealChainMessage(t, h, m, f.recipient, f.sender), Message: m, GrantID: m.GrantID, Now: testNow}
}

func TestAskReplyCustodyAndNotificationCommitAtomically(t *testing.T) {
	s, f, reservation := redactedAsk(t)
	reply := askReplyRequest(t, s, f)
	r := askEvidence(reservation, "reply")
	notice := nativeNoticeFixture(t, s, f, r)
	packet, err := protocol.DecodePacket(notice.Packet)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("before atomic responder reply commit")
	s.abort = func(step string) error {
		if step == "before_commit" {
			return injected
		}
		return nil
	}
	if _, err = s.CommitAskReply(t.Context(), reservation.RunID, reply, r, notice); !errors.Is(err, injected) {
		t.Fatalf("commit: %v", err)
	}
	s.abort = nil
	for _, id := range []string{reply.Message.MessageID, packet.Header.MessageID} {
		if exists, existsErr := s.OutboxExists(t.Context(), id); existsErr != nil || exists {
			t.Fatalf("partial reply outbox: %v %v", exists, existsErr)
		}
	}
	if got, readErr := s.Receipt(t.Context(), reservation.MessageID); readErr != nil || got.Stage != "reply_redacted" {
		t.Fatalf("partial receipt: %+v %v", got, readErr)
	}
	got, err := s.CommitAskReply(t.Context(), reservation.RunID, reply, r, notice)
	if err != nil || got.Stage != "sender_stored" {
		t.Fatalf("reply custody: %+v %v", got, err)
	}
	var state string
	if err = s.db.QueryRowContext(t.Context(), `SELECT state FROM responder_runs WHERE run_id=?`, reservation.RunID).Scan(&state); err != nil || state != "reply_committed" {
		t.Fatalf("run custody: %s %v", state, err)
	}
	if got, readErr := s.Receipt(t.Context(), reservation.MessageID); readErr != nil || got.Stage != "reply" {
		t.Fatalf("missing reply receipt: %+v %v", got, readErr)
	}
	if _, err = s.OutboxPacket(t.Context(), packet.Header.MessageID); err != nil {
		t.Fatal(err)
	}
}

func TestAskReplyRejectsAlteredRetainedChain(t *testing.T) {
	for _, field := range []string{"parent", "reply_to", "conversation", "budget", "hop"} {
		t.Run(field, func(t *testing.T) {
			s, f, reservation := redactedAsk(t)
			reply := askReplyRequest(t, s, f)
			switch field {
			case "parent":
				reply.Message.ParentID = strings.Repeat("d", 32)
			case "reply_to":
				reply.Message.ReplyTo = strings.Repeat("d", 32)
			case "conversation":
				reply.Message.ConversationID = strings.Repeat("d", 32)
			case "budget":
				reply.Message.BudgetID = "fresh_budget"
			case "hop":
				reply.Message.Hop++
			}
			if _, err := s.CommitAskReply(t.Context(), reservation.RunID, reply, askEvidence(reservation, "reply")); !fault.IsCode(err, fault.ScopeDenied) {
				t.Fatalf("altered chain: %v", err)
			}
			if exists, err := s.OutboxExists(t.Context(), reply.Message.MessageID); err != nil || exists {
				t.Fatalf("partial reply: %v %v", exists, err)
			}
		})
	}
}

func TestAskReplyDiscardsLateOutput(t *testing.T) {
	for _, reason := range []string{"cancelled", "revoked", "expired", "binding_changed", "lease_changed", "recovered"} {
		t.Run(reason, func(t *testing.T) {
			s, f, reservation := redactedAsk(t)
			reply := askReplyRequest(t, s, f)
			r := askEvidence(reservation, "reply")
			switch reason {
			case "cancelled":
				if err := s.AdvanceAsk(t.Context(), reservation.RunID, askEvidence(reservation, "cancelled"), nil); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				if _, err := s.db.ExecContext(t.Context(), `UPDATE grants SET revoked=1 WHERE id='grant_one'`); err != nil {
					t.Fatal(err)
				}
			case "expired":
				reply.Now, r.At = testNow+300, testNow+300
			case "binding_changed":
				if _, err := s.db.ExecContext(t.Context(), `UPDATE bindings SET state='needs_rebind'`); err != nil {
					t.Fatal(err)
				}
			case "lease_changed":
				if _, err := s.db.ExecContext(t.Context(), `UPDATE grants SET lease_generation=lease_generation+1 WHERE id='grant_one'`); err != nil {
					t.Fatal(err)
				}
			case "recovered":
				if err := s.RecoverAskRuns(t.Context(), testNow+1); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.CommitAskReply(t.Context(), reservation.RunID, reply, r); err == nil {
				t.Fatal("late output committed")
			}
			if exists, err := s.OutboxExists(t.Context(), reply.Message.MessageID); err != nil || exists {
				t.Fatalf("late reply outbox: %v %v", exists, err)
			}
		})
	}
}

func TestAskProgressRequiresOrderedTrustedStages(t *testing.T) {
	s, _, r := askFixture(t)
	reserveAsk(t, s, r)
	if err := s.AdvanceAsk(t.Context(), r.RunID, askEvidence(r, "responder_started"), []byte("child-id")); !fault.IsCode(err, InvalidState) {
		t.Fatalf("unauthorized start: %v", err)
	}
	if err := s.AuthorizeAsk(t.Context(), r.RunID, r.Now); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceAsk(t.Context(), r.RunID, askEvidence(r, "responder_started"), []byte("child-id")); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceAsk(t.Context(), r.RunID, askEvidence(r, "turn_completed"), nil); !fault.IsCode(err, InvalidState) {
		t.Fatalf("completion without input: %v", err)
	}
	if err := s.AdvanceAsk(t.Context(), r.RunID, askEvidence(r, "model_input"), []byte("other-child")); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("native child swapped: %v", err)
	}
}
