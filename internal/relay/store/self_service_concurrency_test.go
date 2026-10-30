package store

import (
	"strconv"
	"sync"
	"testing"
)

type selfServiceResult struct {
	id      string
	mailbox Mailbox
	err     error
}

func TestSelfServiceConcurrentAdmissionCannotOverrunAnyPerKeyQuota(t *testing.T) {
	for _, test := range []struct {
		name   string
		policy SelfServicePolicy
	}{
		{"active", SelfServicePolicy{MaxActiveInvites: 2, MaxInvitesPer24h: 20, MaxInvitesLifetime: 128, MaxInviters: 100}},
		{"daily", SelfServicePolicy{MaxActiveInvites: 4, MaxInvitesPer24h: 2, MaxInvitesLifetime: 128, MaxInviters: 100}},
		{"lifetime", SelfServicePolicy{MaxActiveInvites: 4, MaxInvitesPer24h: 20, MaxInvitesLifetime: 2, MaxInviters: 100}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSelfServiceFixture(t, Limits{})
			key := selfServiceKey(0)
			start := make(chan struct{})
			results := make(chan selfServiceResult, 16)
			var wg sync.WaitGroup
			for i := range 16 {
				wg.Go(func() {
					<-start
					id := "racing-" + strconv.Itoa(i)
					mailbox, err := f.s.CreateOwnInvite(t.Context(), key, f.invite(id), test.policy)
					results <- selfServiceResult{id, mailbox, err}
				})
			}
			close(start)
			wg.Wait()
			close(results)
			accepted := 0
			for result := range results {
				if result.err == nil {
					accepted++
					if result.mailbox != (Mailbox{ID: selfServiceZeroMailbox, ReadKey: key}) {
						t.Fatalf("racing publication changed mailbox: %+v", result.mailbox)
					}
					if err := f.s.CheckInviteOwner(t.Context(), result.id, key); err != nil {
						t.Fatal(err)
					}
				} else {
					selfServiceRequireNoCreation(t, result.mailbox, result.err, 429)
					_, err := f.s.GetInvite(t.Context(), result.id, f.now)
					requireStatus(t, err, 410)
				}
			}
			if accepted != 2 {
				t.Fatalf("quota admitted %d concurrent invitations, want 2", accepted)
			}
			f.reopen(t)
			mailbox, err := f.s.CreateOwnInvite(t.Context(), key, f.invite("after-restart"), test.policy)
			selfServiceRequireNoCreation(t, mailbox, err, 429)
		})
	}
}

func TestSelfServiceConcurrentExactRetryCommitsOneAdmission(t *testing.T) {
	// Twelve global slots leave exactly three in the self-service quarter.
	f := newSelfServiceFixture(t, Limits{MetadataItems: 12})
	policy := SelfServicePolicy{MaxActiveInvites: 1, MaxInvitesPer24h: 1, MaxInvitesLifetime: 1, MaxInviters: 1}
	key, invite := selfServiceKey(0), f.invite("one-commit")
	start := make(chan struct{})
	results := make(chan selfServiceResult, 16)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			<-start
			mailbox, err := f.s.CreateOwnInvite(t.Context(), key, invite, policy)
			results <- selfServiceResult{invite.ID, mailbox, err}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	for result := range results {
		if result.err != nil || result.mailbox != (Mailbox{ID: selfServiceZeroMailbox, ReadKey: key}) {
			t.Fatalf("concurrent exact retry: %+v, %v", result.mailbox, result.err)
		}
	}
	f.reopen(t)
	f.requireDescriptor(t, invite)
	mailbox, err := f.s.CreateOwnInvite(t.Context(), key, f.invite("second"), policy)
	selfServiceRequireNoCreation(t, mailbox, err, 429)
}

func TestSelfServiceConcurrentNewKeysCannotOverrunGlobalRegistry(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	policy := selfServicePolicy()
	policy.MaxInviters = 2
	start := make(chan struct{})
	results := make(chan selfServiceResult, 8)
	var wg sync.WaitGroup
	for i := range uint8(8) {
		wg.Go(func() {
			<-start
			id := "key-" + strconv.Itoa(int(i))
			mailbox, err := f.s.CreateOwnInvite(t.Context(), selfServiceKey(i), f.invite(id), policy)
			results <- selfServiceResult{id, mailbox, err}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	accepted := 0
	for result := range results {
		if result.err == nil {
			accepted++
		} else {
			selfServiceRequireNoCreation(t, result.mailbox, result.err, 429)
			_, err := f.s.GetInvite(t.Context(), result.id, f.now)
			requireStatus(t, err, 410)
		}
	}
	if accepted != 2 {
		t.Fatalf("registry admitted %d new keys, want 2", accepted)
	}
	f.reopen(t)
	mailbox, err := f.s.CreateOwnInvite(t.Context(), selfServiceKey(9), f.invite("after-registry-restart"), policy)
	selfServiceRequireNoCreation(t, mailbox, err, 429)
}
