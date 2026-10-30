package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSelfServiceVoucherConflictCannotLeavePartialAdmission(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	legacy := f.invite("voucher-already-used")
	if err := f.s.PutInvite(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	policy := selfServicePolicy()
	policy.MaxInviters = 1
	invite := f.invite("new-voucher-conflict")
	invite.Voucher = legacy.Voucher
	mailbox, err := f.s.CreateOwnInvite(t.Context(), selfServiceKey(0), invite, policy)
	// This exercises the existing unique voucher constraint without prescribing
	// whether the store detects the conflict before INSERT or rolls INSERT back.
	if err == nil || mailbox != (Mailbox{}) {
		t.Fatalf("duplicate voucher admitted or exposed partial mailbox: %+v, %v", mailbox, err)
	}
	requireStatus(t, f.s.CheckReader(t.Context(), selfServiceZeroMailbox, selfServiceKey(0)), 403)
	_, err = f.s.GetInvite(t.Context(), invite.ID, f.now)
	requireStatus(t, err, 410)
	f.requireDescriptor(t, legacy)
	// A leaked registry consumes the only slot even though its invite failed.
	f.create(t, selfServiceKey(1), "after-voucher-conflict", policy)
	f.reopen(t)
	requireStatus(t, f.s.CheckInviteOwner(t.Context(), invite.ID, selfServiceKey(0)), 403)
}

func TestSelfServiceEnrollmentVoucherIsNotPersistedInPlaintext(t *testing.T) {
	f := newSelfServiceFixture(t, Limits{})
	_, invite := f.create(t, selfServiceKey(0), "privacy-positive-control", selfServicePolicy())
	reader := selfServiceKey(1)
	f.claim(t, invite, reader)
	if err := f.s.CreateMailbox(t.Context(), selfServiceMailbox(t, reader), invite.Voucher); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	root, openErr := os.OpenRoot(filepath.Dir(f.path))
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	foundPacket := false
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, readErr := root.ReadFile(filepath.Base(f.path) + suffix)
		if os.IsNotExist(readErr) && suffix != "" {
			continue
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		if bytes.Contains(data, []byte(invite.Voucher)) {
			t.Fatal("self-service persisted plaintext enrollment authority")
		}
		foundPacket = foundPacket || bytes.Contains(data, invite.Packet)
	}
	if !foundPacket {
		t.Fatal("privacy scan did not find its opaque packet positive control")
	}
	f.open(t)
	if err := f.s.CheckInviteOwner(t.Context(), invite.ID, selfServiceKey(0)); err != nil {
		t.Fatal(err)
	}
}
