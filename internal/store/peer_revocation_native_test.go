package store

import (
	"encoding/json"
	"testing"

	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestNativeRevokeReceiptClosesOutboundGrant(t *testing.T) {
	for _, test := range []struct {
		reason, evidence, outcome string
		want                      bool
	}{
		{"revoked", "local_revoke", "already_admitted", true},
		{"revoked", "local_revoke", "submit_unknown", true},
		{"timeout", "local_revoke", "already_admitted", false},
		{"revoked", "native_queue", "already_admitted", false},
		{"revoked", "local_revoke", "unknown", false},
	} {
		t.Run(test.reason+"_"+test.evidence+"_"+test.outcome, func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, 1)); err != nil {
				t.Fatal(err)
			}
			o := installReturnGrant(t, s, f)
			body, err := json.Marshal(protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: f.message.MessageID, Stage: "in_doubt", EvidenceKind: test.evidence, ReasonCode: test.reason, Outcome: test.outcome, At: testNow})
			if err != nil {
				t.Fatal(err)
			}
			request := returnReply(t, f, o, func(m *protocol.MessagePayload) {
				m.Kind, m.ParentID, m.ReplyTo, m.Hop, m.BodyUTF8 = protocol.Receipt, "", "", 0, string(body)
			})
			if _, err = s.InboxCommit(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			revoked, err := s.RevokedOutboundGrant(t.Context(), "grant_one")
			if err != nil || revoked != test.want {
				t.Fatalf("authenticated native revoke=%v want %v: %v", revoked, test.want, err)
			}
		})
	}
}
