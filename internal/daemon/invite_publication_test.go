package daemon

import (
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/store"
)

func publicationEntry(t *testing.T, d *Daemon) *issuedInvite {
	t.Helper()
	if len(d.state.Issued) != 1 {
		t.Fatal("fixture requires one retained synthetic invitation")
	}
	for _, entry := range d.state.Issued {
		return entry
	}
	return nil
}

func TestInvitePublicationIdleACKIsTransientAcrossRestart(t *testing.T) {
	parallelDaemonTest(t)
	f := newSelfServiceHTTPPeer(t, `"self_service"`)
	d := openSelfServiceDaemon(t, f, nil)
	if err := requestSelfServiceInvite(t, d); err != nil {
		t.Fatal("initial publication failed:", safeCode(err))
	}
	before := selfServiceJournal(t, d, true)
	entry := publicationEntry(t, d)
	if !entry.publication.confirmed || publicationEntryFromState(t, before).publication != (invitePublication{}) {
		t.Fatal("validated ACK was not transient or was serialized")
	}
	for range 8 {
		if err := d.Sync(t.Context()); err != nil {
			t.Fatal("idle polling failed:", safeCode(err))
		}
	}
	assertSelfServiceRetry(t, f, 1, 1)
	if f.snapshot().poll != 8 || len(statusFor(d.state, "", 0, "").OnboardingErrors) != 0 {
		t.Fatal("idle confirmation stopped Fetch or hid an onboarding error")
	}
	d = reopenSelfServiceDaemon(t, d)
	if publicationEntry(t, d).publication != (invitePublication{}) {
		t.Fatal("restart guessed a publication ACK")
	}
	if err := d.Sync(t.Context()); err != nil {
		t.Fatal("restart did not replay retained publication:", safeCode(err))
	}
	assertSelfServiceRetry(t, f, 2, 1)
	assertSelfServiceRetained(t, before, d, true)
	assertSelfServiceOwnMailboxNoPeerAuthority(t, d)
}

func publicationEntryFromState(t *testing.T, state clientState) *issuedInvite {
	t.Helper()
	return publicationEntry(t, &Daemon{state: state})
}

func TestInvitePublicationLostOrInvalidACKCannotSkipReplay(t *testing.T) {
	parallelDaemonTest(t)
	for _, outcome := range []string{"lost", "invalid"} {
		t.Run(outcome, func(t *testing.T) {
			f := newSelfServiceHTTPPeer(t, `"self_service"`)
			f.mu.Lock()
			f.dropACK = outcome == "lost"
			if outcome == "invalid" {
				f.badACK = `{"id":"$id"}`
			}
			f.mu.Unlock()
			d := openSelfServiceDaemon(t, f, nil)
			want := "transport_unavailable"
			if outcome == "invalid" {
				want = "invalid_response"
			}
			if err := requestSelfServiceInvite(t, d); safeCode(err) != want {
				t.Fatal("fixture did not lose/refuse its publication ACK:", safeCode(err))
			}
			entry := publicationEntry(t, d)
			before := selfServiceJournal(t, d, false)
			if entry.publication.confirmed || entry.Failure != want {
				t.Fatal("unvalidated publication guessed confirmation or lost its cause")
			}
			entry.publication.retryAt = time.Now().Add(time.Minute)
			f.mu.Lock()
			f.badACK = ""
			f.mu.Unlock()
			if err := d.Sync(t.Context()); err != nil {
				t.Fatal("deferred publication unexpectedly fenced custody:", safeCode(err))
			}
			assertSelfServiceRetry(t, f, 1, 1)
			selfServiceJournal(t, d, false)
			if entry.Failure != want || entry.publication.confirmed {
				t.Fatal("deferred attempt was treated as successful enrollment")
			}
			// Advance only the transient retry gate; retain all publication bytes.
			entry.publication.retryAt = time.Time{}
			if err := d.Sync(t.Context()); err != nil {
				t.Fatal("due exact publication failed:", safeCode(err))
			}
			assertSelfServiceRetry(t, f, 2, 1)
			assertSelfServiceRetained(t, before, d, true)
			assertSelfServiceOwnMailboxNoPeerAuthority(t, d)
		})
	}
}

func TestInvitePublicationACKBeforeFailedEnrollmentSaveIsNotConfirmed(t *testing.T) {
	parallelDaemonTest(t)
	f := newSelfServiceHTTPPeer(t, `"self_service"`)
	d := openSelfServiceDaemon(t, f, nil)
	f.mu.Lock()
	f.beforeACK = func() {
		d.mu.Lock()
		d.sealer = failedSealer{d.sealer}
		d.mu.Unlock()
	}
	f.mu.Unlock()
	if err := requestSelfServiceInvite(t, d); !fault.IsCode(err, store.SealingFailed) {
		t.Fatal("fixture did not fail the required enrollment save")
	}
	saved, err := loadState(d.options.StateDir, d.sealer)
	if err != nil || saved.Enrolled || publicationEntry(t, d).publication.confirmed || !d.persistenceFailed {
		t.Fatal("publication confirmation preceded its required durable enrollment")
	}
	assertSelfServiceOwnMailboxNoPeerAuthority(t, d)
}

func TestInvitePublicationConfirmationBindsExactRetainedMaterial(t *testing.T) {
	parallelDaemonTest(t)
	for _, change := range []string{"packet", "voucher", "expiry"} {
		t.Run(change, func(t *testing.T) {
			f := newSelfServiceHTTPPeer(t, `"self_service"`)
			d := openSelfServiceDaemon(t, f, nil)
			if err := requestSelfServiceInvite(t, d); err != nil {
				t.Fatal("initial publication failed:", safeCode(err))
			}
			entry := publicationEntry(t, d)
			switch change {
			case "packet":
				entry.Packet.Ciphertext[0] ^= 1
			case "voucher":
				entry.Descriptor.EnrollmentVoucher = randomID() + randomID()
			case "expiry":
				entry.Descriptor.ExpiresAt--
			}
			if err := d.ensureInvitePublished(t.Context(), entry); safeCode(err) != "conflict" {
				t.Fatal("changed publication reused an earlier confirmation:", safeCode(err))
			}
			if f.snapshot().publish != 2 || entry.publication.confirmed {
				t.Fatal("changed exact publication bypassed relay revalidation")
			}
			assertSelfServiceOwnMailboxNoPeerAuthority(t, d)
		})
	}
}
