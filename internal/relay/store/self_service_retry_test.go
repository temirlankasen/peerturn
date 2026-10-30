package store

import (
	"bytes"
	"testing"
	"time"
)

func TestSelfServiceExactRetryIsImmutableAndDoesNotSpendAgain(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	key := selfServiceKey(0)
	policy := SelfServicePolicy{MaxActiveInvites: 1, MaxInvitesPer24h: 1, MaxInvitesLifetime: 1, MaxInviters: 1}
	mailbox, invite := f.create(t, key, "retry", policy)
	f.now = f.now.Add(30 * time.Second)
	f.reopen(t)
	got, err := f.s.CreateOwnInvite(t.Context(), key, invite, policy)
	if err != nil || got != mailbox {
		t.Fatalf("exact retry at full quotas changed result: %+v, %v", got, err)
	}
	f.requireDescriptor(t, invite)
	for _, test := range []struct {
		name   string
		change func(*Invite)
	}{
		{"packet", func(i *Invite) { i.Packet = []byte("changed-ciphertext") }},
		{"proof", func(i *Invite) { i.ProofKey = selfServiceKey(241) }},
		{"expiry", func(i *Invite) { i.ExpiresAt = i.ExpiresAt.Add(time.Second) }},
		{"voucher", func(i *Invite) { i.Voucher = "changed-voucher" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := invite
			test.change(&changed)
			result, createErr := f.s.CreateOwnInvite(t.Context(), key, changed, policy)
			selfServiceRequireNoCreation(t, result, createErr, 409)
			f.requireDescriptor(t, invite)
		})
	}
	got, err = f.s.CreateOwnInvite(t.Context(), selfServiceKey(1), invite, policy)
	selfServiceRequireNoCreation(t, got, err, 409)
	got, err = f.s.CreateOwnInvite(t.Context(), key, f.invite("second"), policy)
	selfServiceRequireNoCreation(t, got, err, 429)
}

func TestSelfServiceExpiredAndRevokedInvitesCannotBeRepublished(t *testing.T) {
	for _, terminal := range []string{"expiry", "revoke"} {
		t.Run(terminal, func(t *testing.T) {
			f := newSelfServiceFixture(t, Limits{})
			key, policy := selfServiceKey(0), selfServicePolicy()
			_, invite := f.create(t, key, "terminal", policy)
			if terminal == "expiry" {
				f.now = invite.ExpiresAt
				if err := f.s.Sweep(t.Context(), f.now); err != nil {
					t.Fatal(err)
				}
			} else if err := f.s.RevokeOwnedInvite(t.Context(), invite.ID, key); err != nil {
				t.Fatal(err)
			}
			f.reopen(t)
			for _, candidate := range []Invite{invite, f.invite(invite.ID)} {
				mailbox, err := f.s.CreateOwnInvite(t.Context(), key, candidate, policy)
				selfServiceRequireNoCreation(t, mailbox, err, 410)
			}
			_, err := f.s.GetInvite(t.Context(), invite.ID, f.now)
			requireStatus(t, err, 410)
			if err := f.s.CheckInviteOwner(t.Context(), invite.ID, key); err != nil {
				t.Fatalf("tombstone lost creator: %v", err)
			}
			if err := f.s.RevokeOwnedInvite(t.Context(), invite.ID, key); err != nil {
				t.Fatalf("retained owner cannot revoke tombstone: %v", err)
			}
		})
	}
}

func TestSelfServiceRevokeRequiresCreatorNotInvitationProofKey(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	key := selfServiceKey(0)
	_, invite := f.create(t, key, "owner", selfServicePolicy())
	for _, wrong := range []string{selfServiceKey(1), invite.ProofKey} {
		requireStatus(t, f.s.CheckInviteOwner(t.Context(), invite.ID, wrong), 403)
		requireStatus(t, f.s.RevokeOwnedInvite(t.Context(), invite.ID, wrong), 403)
		f.requireDescriptor(t, invite)
	}
	if err := f.s.CheckInviteOwner(t.Context(), invite.ID, key); err != nil {
		t.Fatal(err)
	}
	if err := f.s.RevokeOwnedInvite(t.Context(), invite.ID, key); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	if err := f.s.RevokeOwnedInvite(t.Context(), invite.ID, key); err != nil {
		t.Fatalf("owned revoke is not idempotent: %v", err)
	}
	requireStatus(t, f.s.RevokeOwnedInvite(t.Context(), invite.ID, invite.ProofKey), 403)
	_, err := f.s.GetInvite(t.Context(), invite.ID, f.now)
	requireStatus(t, err, 410)
	_, err = f.s.ClaimInvite(t.Context(), invite.ID, Claim{ID: "late", ReaderKey: selfServiceKey(2), Packet: []byte("opaque")}, f.now)
	requireStatus(t, err, 410)
}

func TestSelfServiceUnknownRevokeDoesNotCreatePublicTombstone(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	invite := f.invite("unknown")
	requireStatus(t, f.s.CheckInviteOwner(t.Context(), invite.ID, selfServiceKey(0)), 403)
	requireStatus(t, f.s.RevokeOwnedInvite(t.Context(), invite.ID, selfServiceKey(0)), 403)
	f.reopen(t)
	// Trusted provisioning can still use this ID; unauthorized revoke reserved nothing.
	if err := f.s.PutInvite(t.Context(), invite); err != nil {
		t.Fatal(err)
	}
	f.requireDescriptor(t, invite)
}

func TestSelfServiceNeverAdoptsLegacyOwnershipEvenForMatchingProofAndMailbox(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	key, policy := selfServiceKey(0), selfServicePolicy()
	policy.MaxInviters = 1
	legacy := f.invite("legacy")
	legacy.ProofKey = key
	if err := f.s.CreateMailbox(t.Context(), Mailbox{ID: selfServiceZeroMailbox, ReadKey: key}, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.PutInvite(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	mailbox, err := f.s.CreateOwnInvite(t.Context(), key, legacy, policy)
	selfServiceRequireNoCreation(t, mailbox, err, 409)
	requireStatus(t, f.s.CheckInviteOwner(t.Context(), legacy.ID, key), 403)
	requireStatus(t, f.s.RevokeOwnedInvite(t.Context(), legacy.ID, key), 403)
	f.requireDescriptor(t, legacy)
	f.create(t, selfServiceKey(1), "real-owner", policy)
	// Legacy provisioning keeps its prior semantics and cannot strip new ownership.
	_, owned := f.create(t, selfServiceKey(1), "owned", policy)
	if provisionErr := f.s.PutInvite(t.Context(), owned); provisionErr != nil {
		t.Fatal(provisionErr)
	}
	if ownerErr := f.s.CheckInviteOwner(t.Context(), owned.ID, selfServiceKey(1)); ownerErr != nil {
		t.Fatal(ownerErr)
	}
	got, err := f.s.GetInvite(t.Context(), legacy.ID, f.now)
	if err != nil || !bytes.Equal(got.Packet, legacy.Packet) {
		t.Fatalf("legacy descriptor mutated: %+v, %v", got, err)
	}
}
