package store

import (
	"strconv"
	"testing"
	"time"
)

func TestSelfServiceActiveQuotaIncludesClaimAndReleasesOnlyOnRevokeOrExpiry(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	key, policy := selfServiceKey(0), selfServicePolicy()
	invites := make([]Invite, 4)
	for i := range invites {
		_, invites[i] = f.create(t, key, "active-"+strconv.Itoa(i), policy)
	}
	f.claim(t, invites[0], selfServiceKey(1))
	mailbox, err := f.s.CreateOwnInvite(t.Context(), key, f.invite("claimed-still-active"), policy)
	selfServiceRequireNoCreation(t, mailbox, err, 429)
	if revokeErr := f.s.RevokeOwnedInvite(t.Context(), invites[1].ID, key); revokeErr != nil {
		t.Fatal(revokeErr)
	}
	f.create(t, key, "replacement", policy)
	mailbox, err = f.s.CreateOwnInvite(t.Context(), key, f.invite("still-full"), policy)
	selfServiceRequireNoCreation(t, mailbox, err, 429)
	f.now = f.now.Add(time.Minute)
	if sweepErr := f.s.Sweep(t.Context(), f.now); sweepErr != nil {
		t.Fatal(sweepErr)
	}
	f.reopen(t)
	for i := range 4 {
		f.create(t, key, "after-expiry-"+strconv.Itoa(i), policy)
	}
	mailbox, err = f.s.CreateOwnInvite(t.Context(), key, f.invite("expired-slots-full"), policy)
	selfServiceRequireNoCreation(t, mailbox, err, 429)
}

func TestSelfServiceDailySpendUsesCreationTimeAndSurvivesAllLifecycleChanges(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	started, key, policy := f.now, selfServiceKey(0), selfServicePolicy()
	policy.MaxInvitesPer24h = 3
	var own Mailbox
	invites := make([]Invite, 3)
	for i := range invites {
		own, invites[i] = f.create(t, key, "daily-"+strconv.Itoa(i), policy)
	}
	f.now = started.Add(30 * time.Second)
	if _, err := f.s.CreateOwnInvite(t.Context(), key, invites[0], policy); err != nil {
		t.Fatalf("retry spent daily quota: %v", err)
	}
	f.claim(t, invites[1], selfServiceKey(1))
	if err := f.s.RevokeOwnedInvite(t.Context(), invites[2].ID, key); err != nil {
		t.Fatal(err)
	}
	f.now = started.Add(61 * time.Second)
	if err := f.s.Sweep(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
	if err := f.s.DeleteMailbox(t.Context(), own.ID, key); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	got, err := f.s.CreateOwnInvite(t.Context(), key, f.invite("deleted"), policy)
	selfServiceRequireNoCreation(t, got, err, 403)
	// An explicitly authorized admin restore may restore the mailbox, not spend.
	if restoreErr := f.s.CreateMailbox(t.Context(), own, ""); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	got, err = f.s.CreateOwnInvite(t.Context(), key, f.invite("not-refunded"), policy)
	selfServiceRequireNoCreation(t, got, err, 429)
	f.now = started.Add(24*time.Hour - time.Nanosecond)
	f.reopen(t)
	got, err = f.s.CreateOwnInvite(t.Context(), key, f.invite("before-window"), policy)
	selfServiceRequireNoCreation(t, got, err, 429)
	f.now = started.Add(24 * time.Hour)
	// The rolling window is (now-24h, now]; retry must not move created_ns.
	for i := range 3 {
		f.create(t, key, "new-day-"+strconv.Itoa(i), policy)
	}
	got, err = f.s.CreateOwnInvite(t.Context(), key, f.invite("new-day-full"), policy)
	selfServiceRequireNoCreation(t, got, err, 429)
}

func TestSelfServiceLifetimeSpendSurvivesRestartClaimRevokeExpiryAndMailboxDeletion(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	started, key, policy := f.now, selfServiceKey(0), selfServicePolicy()
	policy.MaxInvitesLifetime, policy.MaxInvitesPer24h = 3, 2
	own, first := f.create(t, key, "lifetime-1", policy)
	_, second := f.create(t, key, "lifetime-2", policy)
	f.claim(t, first, selfServiceKey(1))
	if err := f.s.RevokeOwnedInvite(t.Context(), second.ID, key); err != nil {
		t.Fatal(err)
	}
	f.now = started.Add(24 * time.Hour)
	if err := f.s.Sweep(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	_, third := f.create(t, key, "lifetime-3", policy)
	if err := f.s.RevokeOwnedInvite(t.Context(), third.ID, key); err != nil {
		t.Fatal(err)
	}
	if err := f.s.DeleteMailbox(t.Context(), own.ID, key); err != nil {
		t.Fatal(err)
	}
	f.now = started.Add(4 * 24 * time.Hour)
	f.reopen(t)
	if err := f.s.CreateMailbox(t.Context(), own, ""); err != nil {
		t.Fatal(err)
	}
	mailbox, err := f.s.CreateOwnInvite(t.Context(), key, f.invite("no-lifetime-reset"), policy)
	selfServiceRequireNoCreation(t, mailbox, err, 429)
	_, err = f.s.GetInvite(t.Context(), "no-lifetime-reset", f.now)
	requireStatus(t, err, 410)
	if err := f.s.CheckInviteOwner(t.Context(), first.ID, key); err != nil {
		t.Fatal(err)
	}
}

func TestSelfServiceRegistryCapIncludesBlockedAndDeletedInvitersAcrossRestart(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	policy := selfServicePolicy()
	policy.MaxInviters = 2
	first, _ := f.create(t, selfServiceKey(0), "first-key", policy)
	f.create(t, selfServiceKey(1), "second-key", policy)
	if err := f.s.BlockInviter(t.Context(), selfServiceKey(0)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.DeleteMailbox(t.Context(), first.ID, first.ReadKey); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(24 * time.Hour)
	f.reopen(t)
	mailbox, err := f.s.CreateOwnInvite(t.Context(), selfServiceKey(2), f.invite("third-key"), policy)
	selfServiceRequireNoCreation(t, mailbox, err, 429)
	requireStatus(t, f.s.CheckReader(t.Context(), selfServiceMailbox(t, selfServiceKey(2)).ID, selfServiceKey(2)), 403)
	_, err = f.s.GetInvite(t.Context(), "third-key", f.now)
	requireStatus(t, err, 410)
	mailbox, err = f.s.CreateOwnInvite(t.Context(), selfServiceKey(0), f.invite("blocked"), policy)
	selfServiceRequireNoCreation(t, mailbox, err, 403)
	f.create(t, selfServiceKey(1), "existing-key", policy)
}

func TestSelfServiceQuotaIsPerKeyAndDoesNotClaimPersonOrSybilProtection(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	policy := SelfServicePolicy{MaxActiveInvites: 1, MaxInvitesPer24h: 1, MaxInvitesLifetime: 1, MaxInviters: 2}
	f.create(t, selfServiceKey(0), "key-a", policy)
	mailbox, err := f.s.CreateOwnInvite(t.Context(), selfServiceKey(0), f.invite("key-a-full"), policy)
	selfServiceRequireNoCreation(t, mailbox, err, 429)
	// There is no person identifier at this boundary: a new key gets its own cap.
	f.create(t, selfServiceKey(1), "key-b", policy)
	mailbox, err = f.s.CreateOwnInvite(t.Context(), selfServiceKey(2), f.invite("global-full"), policy)
	selfServiceRequireNoCreation(t, mailbox, err, 429)
}

func TestSelfServiceNonpositivePolicyCannotEnableUnboundedAdmission(t *testing.T) {
	for _, field := range []string{"all", "active", "daily", "lifetime", "registry"} {
		t.Run(field, func(t *testing.T) {
			f := newSelfServiceFixture(t, Limits{})
			policy := selfServicePolicy()
			switch field {
			case "all":
				policy = SelfServicePolicy{}
			case "active":
				policy.MaxActiveInvites = 0
			case "daily":
				policy.MaxInvitesPer24h = -1
			case "lifetime":
				policy.MaxInvitesLifetime = 0
			case "registry":
				policy.MaxInviters = 0
			}
			mailbox, err := f.s.CreateOwnInvite(t.Context(), selfServiceKey(0), f.invite("invalid-policy"), policy)
			selfServiceRequireNoCreation(t, mailbox, err, 400)
			_, err = f.s.GetInvite(t.Context(), "invalid-policy", f.now)
			requireStatus(t, err, 410)
			valid := selfServicePolicy()
			valid.MaxInviters = 1
			f.create(t, selfServiceKey(1), "valid-policy", valid)
		})
	}
}
