package store

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSelfServiceAtomicOwnMailboxAndInviteSurviveRestart(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	key := selfServiceKey(0)
	mailbox, invite := f.create(t, key, "first", selfServicePolicy())
	if mailbox != (Mailbox{ID: selfServiceZeroMailbox, ReadKey: key}) {
		t.Fatalf("mailbox not derived from raw routing key: %+v", mailbox)
	}
	f.requireDescriptor(t, invite)
	f.reopen(t)
	if err := f.s.CheckReader(t.Context(), mailbox.ID, key); err != nil {
		t.Fatal(err)
	}
	if err := f.s.CheckInviteOwner(t.Context(), invite.ID, key); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, f.s.CheckReader(t.Context(), mailbox.ID, invite.ProofKey), 403)
	requireStatus(t, f.s.CheckReader(t.Context(), "arbitrary-mailbox", key), 403)
	f.requireDescriptor(t, invite)
}

func TestSelfServiceRejectsNoncanonicalRoutingKeyWithoutRegistration(t *testing.T) {
	canonical := selfServiceKey(0)
	// B has nonzero unused pad bits and permissive decoders map it to the same bytes.
	for name, key := range map[string]string{
		"empty": "", "short": "reader", "padded": canonical + "=",
		"pad_bits": strings.Repeat("A", 42) + "B",
		"newline":  canonical + "\n", "oversize": strings.Repeat("A", 44),
		"wrong_alphabet": base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("\xff", 32))),
	} {
		t.Run(name, func(t *testing.T) {
			f := newSelfServiceFixture(t, Limits{})
			policy := selfServicePolicy()
			policy.MaxInviters = 1
			invite := f.invite("invalid-key")
			mailbox, err := f.s.CreateOwnInvite(t.Context(), key, invite, policy)
			selfServiceRequireNoCreation(t, mailbox, err, 400)
			_, err = f.s.GetInvite(t.Context(), invite.ID, f.now)
			requireStatus(t, err, 410)
			requireStatus(t, f.s.CheckReader(t.Context(), selfServiceZeroMailbox, canonical), 403)
			f.create(t, canonical, "valid-key", policy)
		})
	}
}

func TestSelfServiceTTLUsesStoreClockAndHasNoAdmissionSideEffects(t *testing.T) {
	for _, test := range []struct {
		name string
		ttl  time.Duration
		want int
	}{
		{"past", -time.Nanosecond, 400}, {"now", 0, 400},
		{"one_ns", time.Nanosecond, 0}, {"limit", 600 * time.Second, 0},
		{"over_limit", 600*time.Second + time.Nanosecond, 400},
		{"601_seconds", 601 * time.Second, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSelfServiceFixture(t, Limits{})
			key, policy := selfServiceKey(0), selfServicePolicy()
			policy.MaxInviters = 1
			invite := f.invite("ttl")
			invite.ExpiresAt = f.now.Add(test.ttl)
			mailbox, err := f.s.CreateOwnInvite(t.Context(), key, invite, policy)
			if test.want == 0 {
				if err != nil || mailbox.ID != selfServiceZeroMailbox {
					t.Fatalf("valid TTL rejected: %+v, %v", mailbox, err)
				}
				f.requireDescriptor(t, invite)
				return
			}
			selfServiceRequireNoCreation(t, mailbox, err, test.want)
			_, err = f.s.GetInvite(t.Context(), invite.ID, f.now)
			requireStatus(t, err, 410)
			requireStatus(t, f.s.CheckReader(t.Context(), selfServiceZeroMailbox, key), 403)
			f.reopen(t)
			f.create(t, selfServiceKey(1), "after-rejection", policy)
		})
	}
}

func TestSelfServiceQuotaFailureRollsBackRegistryMailboxAndInvite(t *testing.T) {
	// The self-service quarter fits three rows, but ordinary custody leaves
	// only two global slots until its mailbox is deleted.
	f := newSelfServiceFixture(t, Limits{MetadataItems: 12})
	if err := f.s.CreateMailbox(t.Context(), Mailbox{ID: "admin", ReadKey: "admin-key"}, ""); err != nil {
		t.Fatal(err)
	}
	for n := range 9 {
		if err := f.s.PutInvite(t.Context(), f.invite("legacy-reserve-"+strconv.Itoa(n))); err != nil {
			t.Fatal(err)
		}
	}
	policy := selfServicePolicy()
	policy.MaxInviters = 1
	invite := f.invite("does-not-fit")
	mailbox, err := f.s.CreateOwnInvite(t.Context(), selfServiceKey(0), invite, policy)
	selfServiceRequireNoCreation(t, mailbox, err, 429)
	requireStatus(t, f.s.CheckReader(t.Context(), selfServiceZeroMailbox, selfServiceKey(0)), 403)
	_, err = f.s.GetInvite(t.Context(), invite.ID, f.now)
	requireStatus(t, err, 410)
	if deleteErr := f.s.DeleteMailbox(t.Context(), "admin", "admin-key"); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	// Exactly three rows now fit. A leaked registry or pending charge rejects this.
	own, _ := f.create(t, selfServiceKey(1), "fits-after-rollback", policy)
	f.reopen(t)
	if readerErr := f.s.CheckReader(t.Context(), own.ID, own.ReadKey); readerErr != nil {
		t.Fatal(readerErr)
	}
	_, err = f.s.GetInvite(t.Context(), invite.ID, f.now)
	requireStatus(t, err, 410)
}

func TestSelfServiceMailboxBindingConflictCannotTakeOverOrSpendRegistry(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	if err := f.s.CreateMailbox(t.Context(), Mailbox{ID: selfServiceZeroMailbox, ReadKey: selfServiceKey(9)}, ""); err != nil {
		t.Fatal(err)
	}
	policy := selfServicePolicy()
	policy.MaxInviters = 1
	mailbox, err := f.s.CreateOwnInvite(t.Context(), selfServiceKey(0), f.invite("collision"), policy)
	selfServiceRequireNoCreation(t, mailbox, err, 409)
	if readerErr := f.s.CheckReader(t.Context(), selfServiceZeroMailbox, selfServiceKey(9)); readerErr != nil {
		t.Fatal(readerErr)
	}
	_, err = f.s.GetInvite(t.Context(), "collision", f.now)
	requireStatus(t, err, 410)
	f.create(t, selfServiceKey(1), "unrelated", policy)
}

func TestSelfServiceMissingMailboxIsNotAutomaticallyRecreated(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	key, policy := selfServiceKey(0), selfServicePolicy()
	mailbox, invite := f.create(t, key, "before-delete", policy)
	if err := f.s.DeleteMailbox(t.Context(), mailbox.ID, key); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	for _, candidate := range []Invite{invite, f.invite("after-delete")} {
		got, err := f.s.CreateOwnInvite(t.Context(), key, candidate, policy)
		selfServiceRequireNoCreation(t, got, err, 403)
	}
	requireStatus(t, f.s.CheckReader(t.Context(), mailbox.ID, key), 403)
	if err := f.s.CheckInviteOwner(t.Context(), invite.ID, key); err != nil {
		t.Fatal(err)
	}
	if err := f.s.RevokeOwnedInvite(t.Context(), invite.ID, key); err != nil {
		t.Fatal(err)
	}
}
