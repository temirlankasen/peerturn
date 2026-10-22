package store

import (
	"errors"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
)

func TestGrantRevokeLocalDoesNotRequireReturnSigner(t *testing.T) {
	for _, test := range []struct {
		name string
		now  int64
	}{
		{"missing_return_signer", testNow + 1},
		{"expired_return_grant", testNow + 601},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			commitOne(t, s, f, 1)
			outgoing := outgoingRequest(t, f, 2)
			if _, err := s.OutboxEnqueue(t.Context(), outgoing); err != nil {
				t.Fatal(err)
			}
			if test.now > testNow+600 {
				if _, err := grant.Verify(f.reverse, f.sender.cert, f.recipient.cert, 1, test.now); !fault.IsCode(err, fault.Expired) {
					t.Fatalf("return grant fixture remains usable: %v", err)
				}
			}
			var beforeControls, beforeGeneration int
			if err := s.db.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM outbox WHERE lane='control'),lease_generation FROM grants WHERE id='grant_one'`).Scan(&beforeControls, &beforeGeneration); err != nil {
				t.Fatal(err)
			}
			if err := s.GrantRevoke(t.Context(), "grant_one", 1, test.now); !fault.IsCode(err, ReceiptUnavailable) {
				t.Fatalf("notification API silently lost its signer requirement: %v", err)
			}
			if err := s.GrantRevokeLocal(t.Context(), "grant_one", 1, test.now); err != nil {
				t.Fatalf("local owner revoke was blocked by return signer: %v", err)
			}
			for _, id := range []string{f.message.MessageID, outgoing.Message.MessageID} {
				receipt, err := s.Receipt(t.Context(), id)
				if err != nil || receipt.Stage != "revoked" || receipt.EvidenceKind != "local_revoke" || receipt.ReasonCode != "revoked" {
					t.Fatalf("local terminal receipt = %+v, %v", receipt, err)
				}
			}
			if item, err := s.Inbox(t.Context(), f.message.MessageID); err != nil || item.State != "quarantined" {
				t.Fatalf("local quarantine = %+v, %v", item, err)
			}
			if _, err := s.DeliveryIntent(t.Context(), f.message.MessageID, "11111111111111111111111111111111", test.now); !fault.IsCode(err, fault.Revoked) {
				t.Fatalf("revoked inbound retained effect authority: %v", err)
			}
			if _, err := s.OutboxPacket(t.Context(), outgoing.Message.MessageID); !fault.IsCode(err, NotFound) {
				t.Fatalf("revoked outbound remains dispatchable: %v", err)
			}
			for _, epoch := range []uint64{1, 2, 1} {
				if err := s.GrantRevokeLocal(t.Context(), "grant_one", epoch, test.now+1); err != nil {
					t.Fatal(err)
				}
			}
			var epoch, revoked, generation, controls, revocations, audit int
			if err := s.db.QueryRowContext(t.Context(), `SELECT epoch,revoked,lease_generation,(SELECT COUNT(*) FROM outbox WHERE lane='control'),(SELECT COUNT(*) FROM revocations WHERE grant_id='grant_one'),(SELECT COUNT(*) FROM audit WHERE action='grant_revoke' AND reason='notification_unavailable') FROM grants WHERE id='grant_one'`).Scan(&epoch, &revoked, &generation, &controls, &revocations, &audit); err != nil {
				t.Fatal(err)
			}
			if epoch != 2 || revoked != 1 || generation != beforeGeneration+1 || controls != beforeControls || revocations != 1 || audit != 1 {
				t.Fatalf("local revoke lost transaction/idempotency: epoch=%d revoked=%d generation=%d controls=%d revocations=%d audit=%d", epoch, revoked, generation, controls, revocations, audit)
			}
		})
	}
}

func TestGrantRevokeLocalRollbackKeepsAuthorityAndEvidenceTogether(t *testing.T) {
	for _, point := range []string{"revoke_epoch", "revoke_quarantine", "revoke_leases", "before_commit"} {
		t.Run(point, func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			commitOne(t, s, f, 1)
			injected := errors.New("local revoke interrupted")
			s.abort = func(step string) error {
				if step == point {
					return injected
				}
				return nil
			}
			if err := s.GrantRevokeLocal(t.Context(), "grant_one", 1, testNow+1); !errors.Is(err, injected) {
				t.Fatalf("local revoke abort = %v", err)
			}
			s.abort = nil
			if receipt, err := s.Receipt(t.Context(), f.message.MessageID); err != nil || receipt.Stage != "receiver_stored" {
				t.Fatalf("partial terminal evidence = %+v, %v", receipt, err)
			}
			if item, err := s.Inbox(t.Context(), f.message.MessageID); err != nil || item.State != "pending" {
				t.Fatalf("partial quarantine = %+v, %v", item, err)
			}
			var epoch, revoked, revocations int
			if err := s.db.QueryRowContext(t.Context(), `SELECT epoch,revoked,(SELECT COUNT(*) FROM revocations) FROM grants WHERE id='grant_one'`).Scan(&epoch, &revoked, &revocations); err != nil || epoch != 1 || revoked != 0 || revocations != 0 {
				t.Fatalf("partial authority: epoch=%d revoked=%d revocations=%d err=%v", epoch, revoked, revocations, err)
			}
		})
	}
}
