package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/relayclient"
)

func TestSelfServiceDaemonFirstInvitationNeedsOnlyPublicationACK(t *testing.T) {
	parallelDaemonTest(t)
	for _, admin := range []bool{false, true} {
		name := "fresh-publisher"
		if admin {
			name = "admin-publisher"
		}
		t.Run(name, func(t *testing.T) {
			f := newSelfServiceHTTPPeer(t, `"self_service"`)
			f.mu.Lock()
			f.admin = admin
			f.mu.Unlock()
			d := openSelfServiceDaemon(t, f, nil)
			f.mu.Lock()
			f.beforeACK = func() {
				saved, err := loadState(d.options.StateDir, d.sealer)
				if err != nil || saved.Enrolled || len(saved.Issued) != 1 {
					t.Error("publication reached relay before journaling its exact retry material, or enrollment preceded its ACK")
				}
			}
			f.mu.Unlock()
			if err := requestSelfServiceInvite(t, d); err != nil {
				t.Fatalf("first self-service invitation must publish without admin enrollment: %v", err)
			}
			trace := f.snapshot()
			if trace.discovery == 0 || trace.enroll != 0 || trace.publish != 1 || trace.spent != 1 {
				t.Fatal("self-service did not select signed own-publication as its sole admission mutation")
			}
			saved := selfServiceJournal(t, d, true)
			if len(saved.Issued) != 1 || len(saved.Peers) != 0 || len(saved.Joining) != 0 {
				t.Fatal("relay admission was confused with a peer connection")
			}
			for id, entry := range saved.Issued {
				if id != entry.Packet.InviteID || entry.Descriptor.ExpiresAt <= unixNow() || entry.Descriptor.ExpiresAt > unixNow()+600 {
					t.Fatal("publication changed the invitation identity or exceeded the self-service TTL")
				}
			}
			grants, err := d.db.ListGrants(t.Context())
			if err != nil || len(grants) != 0 {
				t.Fatal("a publication ACK must not issue E2E grants", err)
			}
		})
	}
}

func TestSelfServiceDaemonMissingHintPreservesClosedAdminFlow(t *testing.T) {
	parallelDaemonTest(t)
	for _, admin := range []bool{false, true} {
		name := "non-admin-refused"
		if admin {
			name = "legacy-admin"
		}
		t.Run(name, func(t *testing.T) {
			f := newSelfServiceHTTPPeer(t, "")
			f.mu.Lock()
			f.admin = admin
			f.mu.Unlock()
			d := openSelfServiceDaemon(t, f, nil)
			err := requestSelfServiceInvite(t, d)
			trace := f.snapshot()
			if trace.enroll != 1 {
				t.Fatal("missing hint bypassed the existing admin enrollment flow")
			}
			if admin {
				if err != nil || trace.publish != 1 {
					t.Fatal("legacy admin publication failed", err)
				}
				selfServiceJournal(t, d, true)
			} else {
				if safeCode(err) != "forbidden" || trace.publish != 0 || len(d.state.Issued) != 0 {
					t.Fatal("closed non-admin flow attempted self-service or retained an invitation")
				}
				selfServiceJournal(t, d, false)
			}
		})
	}
}

func TestSelfServiceDaemonInvalidHintRefusesBeforeAdmissionEffects(t *testing.T) {
	parallelDaemonTest(t)
	for _, hint := range []string{`"open"`, `true`, `null`} {
		t.Run(hint, func(t *testing.T) {
			f := newSelfServiceHTTPPeer(t, hint)
			f.mu.Lock()
			f.admin = true
			f.mu.Unlock()
			d := openSelfServiceDaemon(t, f, nil)
			if err := requestSelfServiceInvite(t, d); safeCode(err) != "invalid_response" {
				t.Fatal("invalid discovery fell back to an admission mutation", err)
			}
			trace := f.snapshot()
			if trace.enroll != 0 || trace.publish != 0 || len(selfServiceJournal(t, d, false).Issued) != 0 {
				t.Fatal("invalid discovery changed enrollment or invitation custody")
			}
		})
	}
}

func TestSelfServiceDaemonHintCannotOverrideForbiddenPublication(t *testing.T) {
	parallelDaemonTest(t)
	f := newSelfServiceHTTPPeer(t, `"self_service"`)
	f.mu.Lock()
	f.status, f.code = 403, "forbidden"
	f.mu.Unlock()
	d := openSelfServiceDaemon(t, f, nil)
	if err := requestSelfServiceInvite(t, d); safeCode(err) != "forbidden" {
		t.Fatal("publication refusal was hidden by the hint", err)
	}
	trace := f.snapshot()
	if trace.enroll != 0 || trace.publish != 1 || trace.spent != 0 {
		t.Fatal("server refusal caused an enrollment fallback or a second mutation")
	}
	if len(selfServiceJournal(t, d, false).Issued) != 1 {
		t.Fatal("refused publication lost its existing journal entry")
	}
	d = reopenSelfServiceDaemon(t, d)
	if err := d.Sync(t.Context()); err != nil && safeCode(err) != "forbidden" {
		t.Fatal(err)
	}
	for _, entry := range selfServiceJournal(t, d, false).Issued {
		if !entry.Terminal || entry.Failure != "forbidden" {
			t.Fatal("forbidden invitation remained an unbounded background retry")
		}
	}
	before := f.snapshot().publish
	if err := d.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.snapshot().publish != before {
		t.Fatal("terminal forbidden publication was submitted again")
	}
}

func TestSelfServiceDaemonHintDoesNotAuthorizeStandaloneEnrollment(t *testing.T) {
	parallelDaemonTest(t)
	f := newSelfServiceHTTPPeer(t, `"self_service"`)
	d := openSelfServiceDaemon(t, f, nil)
	if _, err := d.relay.Discovery(t.Context()); err != nil {
		t.Fatal("valid admission hint was not understood", err)
	}
	if _, err := d.relay.EnrollMailbox(t.Context(), relayclient.MailboxRequest{ID: d.mailbox(), ReadKey: publicKey(d.routingPublic())}); safeCode(err) != "forbidden" {
		t.Fatal("hint made general mailbox signup public", err)
	}
	selfServiceJournal(t, d, false)
	if trace := f.snapshot(); trace.enroll != 1 || trace.publish != 0 || trace.spent != 0 {
		t.Fatal("standalone enrollment attempted implicit publication")
	}
}
