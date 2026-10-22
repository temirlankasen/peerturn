package store

import (
	"errors"
	"fmt"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestRevokeRecordsEveryMessageAndCancelsResponder(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	second := commitOne(t, s, f, 2).MessageID
	const attempt = "11111111111111111111111111111111"
	if _, err := s.DeliveryIntent(t.Context(), f.message.MessageID, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	r := protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: attempt, Stage: "responder_started", EvidenceKind: "native_fork", At: testNow}
	if err := s.AuthorizeDelivery(t.Context(), r.MessageID, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.DeliveryResult(t.Context(), attempt, r, []byte("run-one")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `INSERT INTO responder_runs(run_id,message_id,attempt_id,parent_binding,snapshot_handle,model,effort,profile_hash,budget_id,state) VALUES('run_one',?,?,?,X'00','model','xhigh','profile','budget','running')`, r.MessageID, attempt, bindingKey(f.recipient.cert.Binding)); err != nil {
		t.Fatal(err)
	}
	outgoing := outgoingRequest(t, f, 3)
	if _, err := s.OutboxEnqueue(t.Context(), outgoing); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantRevoke(t.Context(), "grant_one", 1, testNow+1, f.receipt); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{r.MessageID: "in_doubt", second: "revoked", outgoing.Message.MessageID: "revoked"} {
		got, err := s.Receipt(t.Context(), id)
		if err != nil || got.Stage != want {
			t.Fatalf("revoke evidence %s: %+v %v", id, got, err)
		}
		if id == r.MessageID && got.Outcome != "already_admitted" {
			t.Fatalf("native effect limitation lost: %+v", got)
		}
	}
	var attemptState, runState string
	if err := s.db.QueryRowContext(t.Context(), `SELECT a.state,r.state FROM delivery_attempts a JOIN responder_runs r ON r.attempt_id=a.id WHERE a.id=?`, attempt).Scan(&attemptState, &runState); err != nil || attemptState != "in_doubt" || runState != "cancel_requested" {
		t.Fatalf("revoke fences: attempt=%s run=%s err=%v", attemptState, runState, err)
	}
	var messageAudits, outboxAudits, controls int
	if err := s.db.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM audit WHERE action='message_revoke' AND epoch=2 AND digest!=''),(SELECT COUNT(*) FROM audit WHERE action='outbox_revoke' AND epoch=2 AND digest!=''),(SELECT COUNT(*) FROM outbox WHERE lane='control')`).Scan(&messageAudits, &outboxAudits, &controls); err != nil || messageAudits != 2 || outboxAudits != 1 || controls != 4 {
		t.Fatalf("atomic revoke records: messages=%d outgoing=%d controls=%d err=%v", messageAudits, outboxAudits, controls, err)
	}
	r.Stage, r.EvidenceKind, r.NativeTurnID, r.At = "turn_completed", "native_reconciliation", "observed-turn", testNow+2
	if err := s.ReconcileReceipt(t.Context(), InboundReceipt, "grant_one", r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeliveryIntent(t.Context(), r.MessageID, attempt, testNow+2); !fault.IsCode(err, fault.Revoked) {
		t.Fatalf("reconciled effect restored authority: %v", err)
	}
	if item, err := s.Inbox(t.Context(), r.MessageID); err != nil || item.State != "quarantined" {
		t.Fatalf("reconciliation removed quarantine: %+v %v", item, err)
	}
}

func TestRevokeNeedsReceiptsAndBuilderFailureRollsBack(t *testing.T) {
	for _, builderFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing_builder", true: "signer_failure"}[builderFailure], func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			commitOne(t, s, f, 1)
			var builders []ReceiptBuilder
			if builderFailure {
				builders = append(builders, func(protocol.ReceiptPayload) ([]byte, error) { return nil, errors.New("private signer diagnostic") })
			}
			if err := s.GrantRevoke(t.Context(), "grant_one", 1, testNow+1, builders...); !fault.IsCode(err, ReceiptUnavailable) {
				t.Fatalf("unrecorded revoke: %v", err)
			}
			var epoch, revoked, events int
			if err := s.db.QueryRowContext(t.Context(), `SELECT epoch,revoked,(SELECT COUNT(*) FROM audit WHERE action IN ('grant_revoke','message_revoke')) FROM grants WHERE id='grant_one'`).Scan(&epoch, &revoked, &events); err != nil || epoch != 1 || revoked != 0 || events != 0 {
				t.Fatalf("partial revoke: epoch=%d revoked=%d audit=%d err=%v", epoch, revoked, events, err)
			}
			if got, err := s.Receipt(t.Context(), f.message.MessageID); err != nil || got.Stage != "receiver_stored" {
				t.Fatalf("partial receipt: %+v %v", got, err)
			}
		})
	}
}

func TestLeaseGenerationRejectsLateNativeResult(t *testing.T) {
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
	if _, err := s.db.ExecContext(t.Context(), `UPDATE grants SET lease_generation=lease_generation+1 WHERE id='grant_one'`); err != nil {
		t.Fatal(err)
	}
	r := protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: attempt, Stage: "adapter_accepted", EvidenceKind: "native_queue", At: testNow + 1}
	if err := s.DeliveryResult(t.Context(), attempt, r, []byte("late-generation")); !fault.IsCode(err, fault.Revoked) {
		t.Fatalf("stale native lease result: %v", err)
	}
	if _, err := s.DeliveryIntent(t.Context(), r.MessageID, attempt, testNow+1); !fault.IsCode(err, fault.Revoked) {
		t.Fatalf("stale lease intent retry: %v", err)
	}
	if got, err := s.Receipt(t.Context(), r.MessageID); err != nil || got.Stage != "receiver_stored" {
		t.Fatalf("stale result changed evidence: %+v %v", got, err)
	}
}

func TestRevokeHasControlReserveAndTerminalOutboundEvidence(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	for i := 1; i < MaxControlMessages; i++ {
		id := fmt.Sprintf("%032x", i+1000)
		if _, err := s.db.ExecContext(t.Context(), `INSERT INTO outbox(id,grant_id,digest,packet,lane,stage,expires_at,bytes,created_at,channel) VALUES(?,'grant_one','digest',X'00','control','sender_stored',?,1,?,?)`, id, testNow+300, testNow, channelKey(f.recipient.cert.Binding, f.sender.cert.Binding)); err != nil {
			t.Fatal(err)
		}
	}
	outgoing := outgoingRequest(t, f, 2)
	if _, err := s.OutboxEnqueue(t.Context(), outgoing); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantRevoke(t.Context(), "grant_one", 1, testNow+1, f.receipt); err != nil {
		t.Fatalf("normal control backlog prevented owner revoke: %v", err)
	}
	var notifications int
	if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM outbox WHERE priority=1`).Scan(&notifications); err != nil || notifications != 1 {
		t.Fatalf("terminal reserve: %d %v", notifications, err)
	}
	r := protocol.ReceiptPayload{MessageID: outgoing.Message.MessageID, AttemptID: outgoing.Message.MessageID, Stage: "relay_stored", EvidenceKind: "late_relay", At: testNow + 2}
	if err := s.RecordReceipt(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Receipt(t.Context(), r.MessageID); err != nil || got.Stage != "revoked" {
		t.Fatalf("delayed storage evidence regressed revoke: %+v %v", got, err)
	}
	if _, err := s.OutboxPacket(t.Context(), r.MessageID); !fault.IsCode(err, NotFound) {
		t.Fatalf("revoked outbound resubmission: %v", err)
	}
}
