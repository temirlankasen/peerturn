package store

import (
	"encoding/json"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestPeerReceiptRequiresActualReverseRecipient(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		t.Run(map[bool]string{false: "recipient", true: "other_identity_same_names"}[wrong], func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, 1)); err != nil {
				t.Fatal(err)
			}
			if wrong {
				f.recipient = newTestParty(t, f.recipient.cert.SessionID)
				g := grant.Grant{Version: 1, GrantID: "grant_reverse", Issuer: f.sender.cert.Binding, Subject: f.recipient.cert.Binding, Scope: grant.Coauthor, GrantEpoch: 1, NotBefore: testNow - 5, ExpiresAt: testNow + 600, Limits: grant.DefaultLimits(), ReplyRoute: &f.recipient.cert.Binding}
				var err error
				f.reverse, err = grant.Issue(g, f.sender.key)
				if err != nil {
					t.Fatal(err)
				}
			}
			o := installReturnGrant(t, s, f)
			body, err := json.Marshal(protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: f.message.MessageID, Stage: "receiver_stored", EvidenceKind: "peer_commit", At: testNow})
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
			want := "receiver_stored"
			if wrong {
				want = "sender_stored"
				if got.Stage != "rejected" || got.Reason != fault.AudienceMismatch || got.Frontier != 1 {
					t.Fatalf("wrong peer refusal: %+v", got)
				}
			}
			r, err := s.Receipt(t.Context(), f.message.MessageID)
			if err != nil || r.Stage != want {
				t.Fatalf("peer changed unrelated evidence: %+v %v", r, err)
			}
		})
	}
}

func TestReceiptAADIncludesDirectionGrantAndChannel(t *testing.T) {
	for _, column := range []string{"direction", "grant_id", "channel"} {
		t.Run(column, func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			commitOne(t, s, f, 1)
			installReturnGrant(t, s, f)
			queries := map[string]string{
				"direction": `UPDATE receipts SET direction='outbound' WHERE message_id=?`,
				"grant_id":  `UPDATE receipts SET grant_id='grant_reverse' WHERE message_id=?`,
				"channel":   `UPDATE receipts SET channel='different_channel' WHERE message_id=?`,
			}
			if _, err := s.db.ExecContext(t.Context(), queries[column], f.message.MessageID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Receipt(t.Context(), f.message.MessageID); !fault.IsCode(err, SealingFailed) {
				t.Fatalf("receipt namespace swap: %v", err)
			}
		})
	}
}

func TestInDoubtNeedsExplicitReconciliationAndNeverResubmits(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	const attempt = "11111111111111111111111111111111"
	if _, err := s.DeliveryIntent(t.Context(), f.message.MessageID, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeDelivery(t.Context(), f.message.MessageID, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverInDoubt(t.Context(), testNow+1); err != nil {
		t.Fatal(err)
	}
	r := protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: attempt, Stage: "model_input", EvidenceKind: "native_event", NativeInputID: "verified-input", At: testNow + 2}
	if err := s.RecordReceipt(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Receipt(t.Context(), r.MessageID); err != nil || got.Stage != "in_doubt" {
		t.Fatalf("ordinary observation cleared fence: %+v %v", got, err)
	}
	if err := s.ReconcileReceipt(t.Context(), InboundReceipt, "grant_one", r); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("unqualified reconciliation: %v", err)
	}
	r.EvidenceKind = "native_reconciliation"
	if err := s.ReconcileReceipt(t.Context(), InboundReceipt, "grant_one", r); err != nil {
		t.Fatal(err)
	}
	got, err := s.Receipt(t.Context(), r.MessageID)
	if err != nil || got.Stage != "model_input" || got.NativeInputID != r.NativeInputID {
		t.Fatalf("reconciled evidence: %+v %v", got, err)
	}
	retry, err := s.DeliveryIntent(t.Context(), r.MessageID, attempt, testNow+3)
	if err != nil || retry.Start || retry.State != "model_input" {
		t.Fatalf("reconciliation resubmitted: %+v %v", retry, err)
	}
	if err = s.ReconcileReceipt(t.Context(), InboundReceipt, "grant_one", r); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("non-ambiguous evidence replaced: %v", err)
	}
}
