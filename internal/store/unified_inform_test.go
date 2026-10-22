package store

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestUnifiedInformStagesAcrossAdapters(t *testing.T) {
	for _, adapter := range []string{"claude", "codex"} {
		t.Run(adapter, func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			f.message.Kind = protocol.Inform
			commitOne(t, s, f, 1)
			id := f.message.MessageID
			const attempt = "11111111111111111111111111111111"
			if adapter == "claude" {
				if err := s.ConsumeInform(t.Context(), id, attempt, testNow); err != nil {
					t.Fatal(err)
				}
			} else {
				r := protocol.ReceiptPayload{MessageID: id, AttemptID: attempt, Stage: "offered", Outcome: "offered", EvidenceKind: "hook_notice", At: testNow}
				if err := s.InformResults(t.Context(), []InformResult{{Receipt: r}}); err != nil {
					t.Fatal(err)
				}
			}
			if r, err := s.Receipt(t.Context(), id); err != nil || r.Stage != "offered" || r.Outcome != "offered" {
				t.Fatalf("notice observation: %+v %v", r, err)
			}
			if count, err := s.PendingInformCount(t.Context()); err != nil || count != 0 {
				t.Fatalf("offered notice still pending: %d %v", count, err)
			}
			if err := s.DeliverInform(t.Context(), id, "22222222222222222222222222222222", testNow); err != nil {
				t.Fatal(err)
			}
			if r, err := s.Receipt(t.Context(), id); err != nil || r.Stage != "delivered" || r.Outcome != "delivered" || r.AttemptID != attempt {
				t.Fatalf("body observation lost notice attempt: %+v %v", r, err)
			}
			if err := s.DeliverInform(t.Context(), id, "33333333333333333333333333333333", testNow+1); err != nil {
				t.Fatalf("repeat body read: %v", err)
			}
			if items, err := s.ListInbox(t.Context(), 8); err != nil || len(items) != 1 || items[0].State != "delivered" {
				t.Fatalf("delivered inform no longer readable: %+v %v", items, err)
			}
		})
	}
}

func TestUnifiedInformRevokePreservesTrueObservation(t *testing.T) {
	for _, stage := range []string{"offered", "delivered"} {
		t.Run(stage, func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			f.message.Kind = protocol.Inform
			commitOne(t, s, f, 1)
			id := f.message.MessageID
			if err := s.ConsumeInform(t.Context(), id, id, testNow); err != nil {
				t.Fatal(err)
			}
			if stage == "delivered" {
				if err := s.DeliverInform(t.Context(), id, id, testNow); err != nil {
					t.Fatal(err)
				}
			}
			if receipts, err := s.RevokeReceipts(t.Context(), "grant_one", testNow+1); err != nil || len(receipts) != 0 {
				t.Fatalf("revoke would deny observed inform: %+v %v", receipts, err)
			}
			if err := s.GrantRevokeLocal(t.Context(), "grant_one", 1, testNow+1); err != nil {
				t.Fatal(err)
			}
			if r, err := s.Receipt(t.Context(), id); err != nil || r.Stage != stage || r.Outcome != stage {
				t.Fatalf("revoke erased observation: %+v %v", r, err)
			}
			if items, err := s.ListInbox(t.Context(), 8); err != nil || len(items) != 0 {
				t.Fatalf("revoked inform remained readable: %+v %v", items, err)
			}
			if err := s.Expire(t.Context(), testNow+301); err != nil {
				t.Fatal(err)
			}
			if r, err := s.Receipt(t.Context(), id); err != nil || r.Stage != stage || r.Outcome != stage {
				t.Fatalf("expiry after revoke erased observation: %+v %v", r, err)
			}
		})
	}
}
