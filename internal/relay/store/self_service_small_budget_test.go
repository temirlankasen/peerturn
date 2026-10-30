package store

import (
	"strconv"
	"testing"
)

func TestSelfServiceTinyMetadataQuarterRefusesAtomicAdmission(t *testing.T) {
	for _, limit := range []int{3, 11} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			f := newSelfServiceFixture(t, Limits{MetadataItems: limit})
			key, invite := selfServiceKey(0), f.invite("quarter-too-small")
			policy := selfServicePolicy()
			policy.MaxInviters = 1
			for range 2 {
				mailbox, err := f.s.CreateOwnInvite(t.Context(), key, invite, policy)
				selfServiceRequireNoCreation(t, mailbox, err, 429)
				if got := selfServiceRetainedRows(t, f.s); got != 0 {
					t.Fatalf("tiny quarter left partial admission: %d rows", got)
				}
				requireStatus(t, f.s.CheckReader(t.Context(), selfServiceZeroMailbox, key), 403)
				requireStatus(t, f.s.CheckInviteOwner(t.Context(), invite.ID, key), 403)
				_, err = f.s.GetInvite(t.Context(), invite.ID, f.now)
				requireStatus(t, err, 410)
				f.reopen(t)
			}
			// Raising the budget, rather than granting a minimum floor, allows
			// one different inviter and proves rejection spent no registry slot.
			f.limits.MetadataItems = 12
			f.reopen(t)
			f.create(t, selfServiceKey(1), "quarter-now-fits", policy)
		})
	}
}
