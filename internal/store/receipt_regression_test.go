package store

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestReceiptCannotCreateOrphanEvidence(t *testing.T) {
	s := testStore(t)
	r := protocol.ReceiptPayload{MessageID: "11111111111111111111111111111111", AttemptID: "11111111111111111111111111111111", Stage: "receiver_stored", EvidenceKind: "peer_commit", At: testNow}
	if err := s.RecordReceipt(t.Context(), r); !fault.IsCode(err, NotFound) {
		t.Fatalf("orphan evidence: %v", err)
	}
}

func TestRevokeCurrentEpochRetryDoesNotAdvanceAuthority(t *testing.T) {
	s := testStore(t)
	fixture(t, s)
	for _, epoch := range []uint64{1, 2, 1, 2} {
		if err := s.GrantRevoke(t.Context(), "grant_one", epoch, testNow); err != nil {
			t.Fatal(err)
		}
	}
	var epoch, revocations, events int
	if err := s.db.QueryRowContext(t.Context(), `SELECT epoch,(SELECT COUNT(*) FROM revocations),(SELECT COUNT(*) FROM audit WHERE action='grant_revoke') FROM grants WHERE id='grant_one'`).Scan(&epoch, &revocations, &events); err != nil {
		t.Fatal(err)
	}
	if epoch != 2 || revocations != 1 || events != 1 {
		t.Fatalf("repeated revoke mutated authority: epoch=%d revocations=%d audit=%d", epoch, revocations, events)
	}
}

func TestLateNativeResultAfterRealIntentIsRevoked(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	const attempt = "11111111111111111111111111111111"
	if got, err := s.DeliveryIntent(t.Context(), f.message.MessageID, attempt, testNow); err != nil || !got.Start {
		t.Fatalf("pre-revoke intent: %+v %v", got, err)
	}
	if err := s.GrantRevoke(t.Context(), "grant_one", 1, testNow+1, f.receipt); err != nil {
		t.Fatal(err)
	}
	r := protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: attempt, Stage: "adapter_accepted", EvidenceKind: "native_queue", At: testNow + 2}
	if err := s.DeliveryResult(t.Context(), attempt, r, []byte("late-queue")); !fault.IsCode(err, fault.Revoked) {
		t.Fatalf("late result escaped revoke fence: %v", err)
	}
}

func TestUnknownInviteRevokeIsNotFound(t *testing.T) {
	s := testStore(t)
	if err := s.RevokeInvite(t.Context(), "AAAAAAAAAAAAAAAAAAAAAA"); !fault.IsCode(err, NotFound) {
		t.Fatalf("unknown invite revoke: %v", err)
	}
}
