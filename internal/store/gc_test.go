package store

import (
	"database/sql"
	"testing"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestGCWipesTerminalContentPreservingNativeEvidence(t *testing.T) {
	for _, state := range []string{"adapter_accepted", "rejected", "in_doubt"} {
		t.Run(state, func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			commitOne(t, s, f, 1)
			id, attempt := f.message.MessageID, "11111111111111111111111111111111"
			if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
				t.Fatal(err)
			}
			r := protocol.ReceiptPayload{MessageID: id, AttemptID: attempt, Stage: state, EvidenceKind: "native_queue", At: testNow}
			if err := s.AuthorizeDelivery(t.Context(), id, attempt, testNow); err != nil {
				t.Fatal(err)
			}
			if err := s.DeliveryResult(t.Context(), attempt, r, []byte("native-evidence")); err != nil {
				t.Fatal(err)
			}
			if err := s.Expire(t.Context(), testNow+301); err != nil {
				t.Fatal(err)
			}
			item, err := s.Inbox(t.Context(), id)
			if err != nil || len(item.Body) != 0 || item.State != state {
				t.Fatalf("retained terminal history: %+v %v", item, err)
			}
			var packet, native []byte
			var used int
			if err = s.db.QueryRowContext(t.Context(), `SELECT i.packet,i.bytes,a.native_result FROM inbox i JOIN delivery_attempts a ON a.message_id=i.id WHERE i.id=?`, id).Scan(&packet, &used, &native); err != nil {
				t.Fatal(err)
			}
			plain, err := s.unseal("inbox_packet", id, packet)
			if err != nil || len(plain) != 0 || used != 0 || len(native) == 0 {
				t.Fatalf("packet/evidence retention: packet=%d bytes=%d native=%d %v", len(plain), used, len(native), err)
			}
		})
	}
}

func TestGCPrunesFinishedRowsAndKeepsReplayFloor(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	id, attempt := f.message.MessageID, "11111111111111111111111111111111"
	if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	r := protocol.ReceiptPayload{MessageID: id, AttemptID: attempt, Stage: "rejected", EvidenceKind: "native_queue", At: testNow}
	if err := s.DeliveryResult(t.Context(), attempt, r, []byte("native-evidence")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `INSERT INTO responder_runs(run_id,message_id,attempt_id,parent_binding,snapshot_handle,model,effort,profile_hash,budget_id,state) SELECT 'run_one',?,?,session_id,X'','model','high','profile','budget','completed' FROM bindings LIMIT 1`, id, attempt); err != nil {
		t.Fatal(err)
	}
	if err := s.Expire(t.Context(), retainUntil(f.header.ExpiresAt, testNow)-1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Inbox(t.Context(), id); err != nil {
		t.Fatalf("removed before replay horizon: %v", err)
	}
	if err := s.Expire(t.Context(), retainUntil(f.header.ExpiresAt, testNow)+1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Inbox(t.Context(), id); !fault.IsCode(err, NotFound) {
		t.Fatalf("finished inbox retained after horizon: %v", err)
	}
	for _, query := range []string{`SELECT COUNT(*) FROM delivery_attempts`, `SELECT COUNT(*) FROM responder_runs`, `SELECT COUNT(*) FROM receipts`, `SELECT COUNT(*) FROM outbox`, `SELECT COUNT(*) FROM dedup WHERE channel IS NOT NULL`} {
		var count int
		if err := s.db.QueryRowContext(t.Context(), query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("unpruned rows for %s: %d %v", query, count, err)
		}
	}
	snapshot := gcSnapshot(t, s, f)
	if snapshot.NextSeq != 2 {
		t.Fatalf("lost replay floor: %+v", snapshot)
	}
	_, err := envelope.CheckReplay(snapshot, envelope.ReplayRecord{MessageID: id, SenderSeq: 1, Digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if !fault.IsCode(err, fault.Replay) {
		t.Fatalf("re-admitted pruned sequence: %v", err)
	}
}

func TestGCKeepsSparseTombstoneAboveMissingSequence(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 2)
	if err := s.Expire(t.Context(), retainUntil(f.header.ExpiresAt, testNow)+1); err != nil {
		t.Fatal(err)
	}
	snapshot := gcSnapshot(t, s, f)
	if snapshot.NextSeq != 1 || len(snapshot.Pending) != 0 || len(snapshot.Records) != 1 || snapshot.Records[0].SenderSeq != 2 {
		t.Fatalf("sparse expiry skipped unknown gap: %+v", snapshot)
	}
	commitOne(t, s, f, 1)
	snapshot = gcSnapshot(t, s, f)
	if snapshot.NextSeq != 3 {
		t.Fatalf("expired parked sequence blocked next admission: %+v", snapshot)
	}
}

func TestGCKeepsAmbiguousAttemptMetadataAfterHorizon(t *testing.T) {
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
	if err := s.Expire(t.Context(), retainUntil(f.header.ExpiresAt, testNow)+1); err != nil {
		t.Fatal(err)
	}
	item, err := s.Inbox(t.Context(), id)
	if err != nil || item.State != "in_doubt" || len(item.Body) != 0 {
		t.Fatalf("ambiguous attempt metadata/body: %+v %v", item, err)
	}
	retry, err := s.DeliveryIntent(t.Context(), id, attempt, testNow+2)
	if err != nil || retry.Start || retry.State != "in_doubt" {
		t.Fatalf("GC lost failure fence: %+v %v", retry, err)
	}
}

func TestGCKeepsUnsettledAuthorizationAfterHorizon(t *testing.T) {
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
	if err := s.Expire(t.Context(), retainUntil(f.header.ExpiresAt, testNow)+1); err != nil {
		t.Fatal(err)
	}
	if item, err := s.Inbox(t.Context(), id); err != nil || item.State != "authorized" || len(item.Body) != 0 {
		t.Fatalf("GC removed unsettled authorization: %+v %v", item, err)
	}
	if err := s.RecoverInDoubt(t.Context(), testNow+1); err != nil {
		t.Fatal(err)
	}
	if uncertain, err := s.UncertainDeliveries(t.Context()); err != nil || !uncertain {
		t.Fatalf("GC erased native-call fence: %v %v", uncertain, err)
	}
}

func TestGCAuditRetentionUsesIndependentClock(t *testing.T) {
	s := testStore(t)
	if _, err := s.db.ExecContext(t.Context(), `INSERT INTO audit(at,action,grant_id,epoch,message_id,digest,reason) VALUES(1,'old','',0,'','',''),(?,'recent','',0,'','','')`, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.Expire(t.Context(), testNow+365*24*60*60); err != nil {
		t.Fatal(err)
	}
	var action string
	if err := s.db.QueryRowContext(t.Context(), `SELECT action FROM audit`).Scan(&action); err != nil || action != "recent" {
		t.Fatalf("caller clock erased recent audit: %q %v", action, err)
	}
}

func gcSnapshot(t *testing.T, s *Store, f *testFixture) envelope.ReplaySnapshot {
	t.Helper()
	var snapshot envelope.ReplaySnapshot
	err := s.write(t.Context(), func(tx *sql.Tx) error {
		g, err := s.authorization(t.Context(), tx, "grant_one", testNow)
		if err != nil {
			return err
		}
		o, err := s.openSnapshot(t.Context(), tx, InboxRequest{GrantID: "grant_one", Open: f.options}, g)
		snapshot = o.Replay
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
