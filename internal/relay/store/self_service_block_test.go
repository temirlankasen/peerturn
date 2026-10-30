package store

import (
	"sync"
	"testing"
	"time"
)

func TestSelfServiceBlockUnknownCanonicalKeyReturnsNotFoundWithoutSpending(t *testing.T) {
	// The self-service quarter must fit the later three-row admission.
	f := newSelfServiceFixture(t, Limits{MetadataItems: 12})
	unknown := selfServiceKey(0)
	requireStatus(t, f.s.BlockInviter(t.Context(), unknown), 404)
	f.reopen(t)
	requireStatus(t, f.s.BlockInviter(t.Context(), unknown), 404)
	requireStatus(t, f.s.CheckReader(t.Context(), selfServiceZeroMailbox, unknown), 403)
	if got := selfServiceRetainedRows(t, f.s); got != 0 {
		t.Fatalf("unknown block allocated retained metadata: %d", got)
	}
	policy := selfServicePolicy()
	policy.MaxInviters = 1
	// A full three-row quarter proves unknown block allocated no metadata or
	// registry slot. It also must not leave a block that overrides quota below.
	f.create(t, selfServiceKey(1), "only-registry-slot", policy)
	requireStatus(t, f.s.BlockInviter(t.Context(), unknown), 404)
	mailbox, err := f.s.CreateOwnInvite(t.Context(), unknown, f.invite("unblocked-unknown"), policy)
	selfServiceRequireNoCreation(t, mailbox, err, 429)
}

func TestSelfServiceBlockFencesPublicationClaimAndVoucherEnrollment(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	key, policy := selfServiceKey(0), selfServicePolicy()
	own, unclaimed := f.create(t, key, "unclaimed", policy)
	_, claimed := f.create(t, key, "claimed", policy)
	reader := selfServiceKey(1)
	winning := f.claim(t, claimed, reader)
	if err := f.s.CheckInviteOwner(t.Context(), unclaimed.ID, key); err != nil {
		t.Fatal(err)
	}
	if err := f.s.BlockInviter(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if err := f.s.BlockInviter(t.Context(), key); err != nil {
		t.Fatalf("block is not idempotent: %v", err)
	}
	for _, restarted := range []bool{false, true} {
		if restarted {
			f.reopen(t)
		}
		for _, invite := range []Invite{unclaimed, f.invite("new-after-block")} {
			mailbox, err := f.s.CreateOwnInvite(t.Context(), key, invite, policy)
			selfServiceRequireNoCreation(t, mailbox, err, 403)
		}
		for _, invite := range []Invite{unclaimed, claimed} {
			_, err := f.s.GetInvite(t.Context(), invite.ID, f.now)
			requireStatus(t, err, 410)
		}
		_, err := f.s.ClaimInvite(t.Context(), unclaimed.ID, Claim{ID: "late", ReaderKey: reader, Packet: []byte("opaque")}, f.now)
		requireStatus(t, err, 410)
		_, err = f.s.ClaimInvite(t.Context(), claimed.ID, winning, f.now)
		requireStatus(t, err, 410)
		requireStatus(t, f.s.CreateMailbox(t.Context(), selfServiceMailbox(t, reader), claimed.Voucher), 403)
		requireStatus(t, f.s.CheckReader(t.Context(), selfServiceMailbox(t, reader).ID, reader), 403)
		// A pre-block ownership check is not a mutation authorization lease.
		if err := f.s.RevokeOwnedInvite(t.Context(), unclaimed.ID, key); err != nil {
			t.Fatalf("block prevented owner recovery: %v", err)
		}
	}
	if err := f.s.DeleteMailbox(t.Context(), own.ID, key); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	mailbox, err := f.s.CreateOwnInvite(t.Context(), key, f.invite("deleted-blocked"), policy)
	selfServiceRequireNoCreation(t, mailbox, err, 403)
	// Even explicit trusted restoration of custody cannot clear the block.
	if restoreErr := f.s.CreateMailbox(t.Context(), own, ""); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	mailbox, err = f.s.CreateOwnInvite(t.Context(), key, f.invite("restored-blocked"), policy)
	selfServiceRequireNoCreation(t, mailbox, err, 403)
}

func TestSelfServiceOwnedRevokeFencesAlreadyClaimedVoucherEnrollment(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	key, reader := selfServiceKey(0), selfServiceKey(1)
	_, invite := f.create(t, key, "voucher-revoke", selfServicePolicy())
	f.claim(t, invite, reader)
	if err := f.s.RevokeOwnedInvite(t.Context(), invite.ID, key); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	requireStatus(t, f.s.CreateMailbox(t.Context(), selfServiceMailbox(t, reader), invite.Voucher), 403)
	requireStatus(t, f.s.CheckReader(t.Context(), selfServiceMailbox(t, reader).ID, reader), 403)
}

func TestSelfServiceBlockRacesSerializeWithEachAdmission(t *testing.T) {
	for _, operation := range []string{"publish", "claim", "enroll"} {
		t.Run(operation, func(t *testing.T) {
			f := newSelfServiceFixture(t, Limits{})
			key, reader, policy := selfServiceKey(0), selfServiceKey(1), selfServicePolicy()
			_, invite := f.create(t, key, "racing-base", policy)
			claim := Claim{ID: "racing-claim", ReaderKey: reader, Packet: []byte("opaque-claim")}
			if operation == "enroll" {
				f.claim(t, invite, reader)
			}
			start := make(chan struct{})
			results := make(chan error, 1)
			blocks := make(chan error, 1)
			var wg sync.WaitGroup
			wg.Go(func() {
				<-start
				blocks <- f.s.BlockInviter(t.Context(), key)
			})
			wg.Go(func() {
				<-start
				var err error
				switch operation {
				case "publish":
					_, err = f.s.CreateOwnInvite(t.Context(), key, f.invite("racing-new"), policy)
				case "claim":
					_, err = f.s.ClaimInvite(t.Context(), invite.ID, claim, f.now)
				case "enroll":
					err = f.s.CreateMailbox(t.Context(), selfServiceMailbox(t, reader), invite.Voucher)
				}
				results <- err
			})
			close(start)
			wg.Wait()
			if err := <-blocks; err != nil {
				t.Fatal(err)
			}
			admissionErr := <-results
			if admissionErr != nil {
				status := 403
				if operation == "claim" {
					status = 410
				}
				requireStatus(t, admissionErr, status)
			}
			// An admission that won first may commit; it cannot leave an active
			// invite surviving the later block transaction.
			for _, id := range []string{invite.ID, "racing-new"} {
				_, err := f.s.GetInvite(t.Context(), id, f.now)
				requireStatus(t, err, 410)
			}
			mailbox, err := f.s.CreateOwnInvite(t.Context(), key, f.invite("after-fence"), policy)
			selfServiceRequireNoCreation(t, mailbox, err, 403)
			_, err = f.s.ClaimInvite(t.Context(), invite.ID, claim, f.now)
			requireStatus(t, err, 410)
		})
	}
}

func TestSelfServiceBlockPreservesExistingCustodyAndRoutingPermissions(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	key := selfServiceKey(0)
	own, _ := f.create(t, key, "transport-stays", selfServicePolicy())
	permit := Permit{ID: "routing-permit", WriterKey: "writer", Epoch: 1, ExpiresAt: f.now.Add(time.Hour)}
	if err := f.s.PutPermit(t.Context(), own.ID, key, permit); err != nil {
		t.Fatal(err)
	}
	m := Message{MailboxID: own.ID, PermitID: permit.ID, PermitEpoch: 1, WriterKey: permit.WriterKey,
		ID: "already-in-custody", Packet: []byte("opaque-packet"), ExpiresAt: f.now.Add(time.Hour)}
	if _, err := f.s.PutMessage(t.Context(), m, f.now); err != nil {
		t.Fatal(err)
	}
	if err := f.s.BlockInviter(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	batch, err := f.s.Fetch(t.Context(), own.ID, key, 0, 100, f.now)
	if err != nil || len(batch) != 1 {
		t.Fatalf("block destroyed existing custody: %+v, %v", batch, err)
	}
	if err := f.s.Ack(t.Context(), own.ID, key, 1, f.now); err != nil {
		t.Fatal(err)
	}
	if err := f.s.CheckPermit(t.Context(), own.ID, permit.ID, permit.WriterKey, 1, f.now); err != nil {
		t.Fatalf("inviter block silently revoked existing routing permission: %v", err)
	}
	if err := f.s.RevokePermit(t.Context(), own.ID, key, permit.ID, 2); err != nil {
		t.Fatal(err)
	}
}
