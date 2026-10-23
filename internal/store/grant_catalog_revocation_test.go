package store

import (
	"encoding/json"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func assertCatalogRevocation(t *testing.T, s *Store, id string, local, effective bool) {
	t.Helper()
	items, err := s.GrantCatalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	var catalog []struct {
		GrantID          string `json:"grant_id"`
		Revoked          bool   `json:"revoked"`
		EffectiveRevoked *bool  `json:"effective_revoked"`
	}
	if err = json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	for _, item := range catalog {
		if item.GrantID == id {
			if item.Revoked != local || item.EffectiveRevoked == nil || *item.EffectiveRevoked != effective {
				t.Fatalf("grant %s: local=%v effective=%v; want %v/%v", id, item.Revoked, item.EffectiveRevoked, local, effective)
			}
			return
		}
	}
	t.Fatalf("grant %s missing", id)
}

func TestGrantCatalogEffectiveRevocation(t *testing.T) {
	for _, mode := range []string{"active", "local", "peer", "native_admitted", "native_unknown", "ordinary_in_doubt", "inbound_only", "relay_only"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, 1)); err != nil {
				t.Fatal(err)
			}
			o := installReturnGrant(t, s, f)
			r := protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: f.message.MessageID, Stage: "revoked", EvidenceKind: "local_revoke", ReasonCode: "revoked", At: testNow}
			switch mode {
			case "local":
				if err := s.GrantRevokeLocal(t.Context(), "grant_one", 1, testNow); err != nil {
					t.Fatal(err)
				}
			case "inbound_only":
				commitOne(t, s, f, 2)
				r.MessageID, r.AttemptID = "00000000000000000000000000000002", "00000000000000000000000000000002"
				if err := s.RecordReceipt(t.Context(), r); err != nil {
					t.Fatal(err)
				}
			case "relay_only":
				if err := s.MarkRelayed(t.Context(), f.message.MessageID, testNow); err != nil {
					t.Fatal(err)
				}
			case "peer", "native_admitted", "native_unknown", "ordinary_in_doubt":
				if mode != "peer" {
					r.Stage, r.Outcome = "in_doubt", "already_admitted"
					if mode == "native_unknown" {
						r.Outcome = "submit_unknown"
					}
					if mode == "ordinary_in_doubt" {
						r.EvidenceKind = "native_queue"
					}
				}
				body, err := json.Marshal(r)
				if err != nil {
					t.Fatal(err)
				}
				request := returnReply(t, f, o, func(m *protocol.MessagePayload) {
					m.Kind, m.ParentID, m.ReplyTo, m.Hop, m.BodyUTF8 = protocol.Receipt, "", "", 0, string(body)
				})
				if _, err = s.InboxCommit(t.Context(), request); err != nil {
					t.Fatal(err)
				}
			}
			local := mode == "local"
			effective := local || mode == "peer" || mode == "native_admitted" || mode == "native_unknown"
			assertCatalogRevocation(t, s, "grant_one", local, effective)
			assertCatalogRevocation(t, s, "grant_reverse", false, false)
			var epoch uint64
			var revoked bool
			if err := s.db.QueryRowContext(t.Context(), `SELECT epoch,revoked FROM grants WHERE id='grant_one'`).Scan(&epoch, &revoked); err != nil || revoked != local || epoch != map[bool]uint64{false: 1, true: 2}[local] {
				t.Fatalf("catalog changed enforcement: epoch=%d revoked=%v: %v", epoch, revoked, err)
			}
		})
	}
}

func TestGrantCatalogFailsClosedOnReceiptCorruption(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE receipts SET evidence=X'00' WHERE direction='outbound'`); err != nil {
		t.Fatal(err)
	}
	if items, err := s.GrantCatalog(t.Context()); !fault.IsCode(err, SealingFailed) || items != nil {
		t.Fatalf("catalog hid corrupt revocation evidence: %+v %v", items, err)
	}
}

func TestListGrantsDoesNotReadReceiptEvidence(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE receipts SET evidence=X'00' WHERE direction='outbound'`); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListGrants(t.Context())
	if err != nil || len(items) != 1 || items[0].GrantID != "grant_one" || items[0].Revoked {
		t.Fatalf("receipt corruption blocked the installed grant catalog: %+v %v", items, err)
	}
}
