package store

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestAuthenticatedPeerReconciliationRequiresExactAmbiguousAttempt(t *testing.T) {
	for _, mode := range []string{"not_sent", "sent", "wrong_attempt", "ordinary_event", "revoked_not_sent", "revoked_sent"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, 1)); err != nil {
				t.Fatal(err)
			}
			o := installReturnGrant(t, s, f)
			r := protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: "11111111111111111111111111111111", Stage: "in_doubt", EvidenceKind: "ambiguous_submit", At: testNow}
			revoked := strings.HasPrefix(mode, "revoked_")
			if revoked {
				r.EvidenceKind, r.ReasonCode, r.Outcome = "local_revoke", "revoked", "submit_unknown"
			}
			if err := s.RecordReceipt(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			r.Stage, r.EvidenceKind, r.Outcome, r.At = "receiver_stored", "native_reconciliation", "not_sent", testNow+1
			r.ReasonCode = ""
			want := "receiver_stored"
			switch mode {
			case "sent", "revoked_sent":
				r.Stage, r.Outcome, want = "adapter_accepted", "sent", "adapter_accepted"
			case "wrong_attempt":
				r.AttemptID, want = "22222222222222222222222222222222", "in_doubt"
			case "ordinary_event":
				r.EvidenceKind, want = "native_event", "in_doubt"
			}
			body, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			request := returnReply(t, f, o, func(m *protocol.MessagePayload) {
				m.Kind, m.ParentID, m.ReplyTo, m.Hop, m.BodyUTF8 = protocol.Receipt, "", "", 0, string(body)
			})
			got, err := s.InboxCommit(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "wrong_attempt" && (got.Stage != "rejected" || got.Reason != fault.InvalidGrant || got.Frontier != 1) {
				t.Fatalf("mismatched reconciliation accepted: %+v", got)
			}
			if receipt, receiptErr := s.Receipt(t.Context(), r.MessageID); receiptErr != nil || receipt.Stage != want {
				t.Fatalf("reverse receipt: %+v %v", receipt, receiptErr)
			}
			var stage string
			if err = s.db.QueryRowContext(t.Context(), `SELECT stage FROM outbox WHERE id=?`, r.MessageID).Scan(&stage); err != nil || stage != want {
				t.Fatalf("outbox stage: %s %v", stage, err)
			}
			if grantRevoked, checkErr := s.RevokedOutboundGrant(t.Context(), "grant_one"); checkErr != nil || grantRevoked != revoked {
				t.Fatalf("reconciliation changed revoked authority: %v %v", grantRevoked, checkErr)
			}
			assertCatalogRevocation(t, s, "grant_one", false, revoked)
			if mode == "revoked_sent" {
				r.Stage, r.EvidenceKind, r.Outcome = "model_input", "native_event", ""
				if err = s.RecordReceipt(t.Context(), r); err != nil {
					t.Fatal(err)
				}
				if grantRevoked, checkErr := s.RevokedOutboundGrant(t.Context(), "grant_one"); checkErr != nil || !grantRevoked {
					t.Fatalf("native progress revived revoked authority: %v %v", grantRevoked, checkErr)
				}
			}
			if mode != "not_sent" {
				return
			}
			r.AttemptID, r.Stage, r.EvidenceKind, r.Outcome = "22222222222222222222222222222222", "adapter_accepted", "native_queue", ""
			body, err = json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			request = returnReply(t, f, o, func(m *protocol.MessagePayload) {
				m.MessageID, m.SenderSeq = strings.Repeat("d", 32), 2
				m.Kind, m.ParentID, m.ReplyTo, m.Hop, m.BodyUTF8 = protocol.Receipt, "", "", 0, string(body)
			})
			request.MailboxSeq = 2
			if got, err = s.InboxCommit(t.Context(), request); err != nil || got.Stage != "receiver_stored" {
				t.Fatalf("fresh native attempt refused: %+v %v", got, err)
			}
			if receipt, receiptErr := s.Receipt(t.Context(), r.MessageID); receiptErr != nil || receipt.Stage != "adapter_accepted" || receipt.AttemptID != r.AttemptID {
				t.Fatalf("fresh native attempt evidence: %+v %v", receipt, receiptErr)
			}
		})
	}
}
