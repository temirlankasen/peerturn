package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/temirlankasen/peerturn/internal/relayclient"
)

// These signed fake HTTPS responses exercise client state, not deployed relay
// admission, native session binding or model delivery.
func TestSelfServiceOwnMailboxDaemonStaleHintRejectsLegacyACK(t *testing.T) {
	parallelDaemonTest(t)
	f := newSelfServiceHTTPPeer(t, `"self_service"`)
	f.mu.Lock()
	f.admin = true
	f.badACK = `{"id":"$id"}`
	f.mu.Unlock()
	d := openSelfServiceDaemon(t, f, nil)
	f.mu.Lock()
	f.beforeACK = func() {
		// publish invokes this callback with f.mu held. Discovery has already
		// advertised self-service, but publication now gets a legacy id-only ACK.
		f.hint = ""
		saved, err := loadState(d.options.StateDir, d.sealer)
		if err != nil || saved.Enrolled || len(saved.Issued) != 1 {
			t.Error("publication preceded its journal or guessed enrollment before ACK")
		}
	}
	f.mu.Unlock()
	err := requestSelfServiceInvite(t, d)
	saved, loadErr := loadState(d.options.StateDir, d.sealer)
	// Check the actual reported defect before asserting the upcoming wire field.
	if loadErr != nil || d.state.Enrolled || saved.Enrolled {
		t.Fatal("stale self-service hint plus legacy id-only ACK persisted unproved Enrolled=true")
	}
	if safeCode(err) != "invalid_response" {
		t.Fatal("legacy publication ACK did not refuse missing own-mailbox proof:", safeCode(err))
	}
	assertSelfServiceRetry(t, f, 1, 1)
	assertSelfServiceOwnMailboxFlag(t, f, true)
	assertSelfServiceOwnMailboxNoPeerAuthority(t, d)
	d = reopenSelfServiceDaemon(t, d)
	assertSelfServiceRetained(t, saved, d, false)
}

func TestSelfServiceOwnMailboxDaemonCorrectACKPersistsAdmission(t *testing.T) {
	parallelDaemonTest(t)
	for _, admin := range []bool{false, true} {
		name := "fresh-publisher"
		if admin {
			name = "admin-without-mailbox"
		}
		t.Run(name, func(t *testing.T) {
			f := newSelfServiceHTTPPeer(t, `"self_service"`)
			f.mu.Lock()
			f.admin = admin
			f.badACK = selfServiceOwnMailboxACK(f.key[32:])
			f.mu.Unlock()
			d := openSelfServiceDaemon(t, f, nil)
			f.mu.Lock()
			f.beforeACK = func() {
				saved, err := loadState(d.options.StateDir, d.sealer)
				if err != nil || saved.Enrolled || len(saved.Issued) != 1 {
					t.Error("own-mailbox admission preceded its ACK or retained publication")
				}
			}
			f.mu.Unlock()
			if err := requestSelfServiceInvite(t, d); err != nil {
				t.Fatal("valid signer-derived own-mailbox ACK was rejected:", safeCode(err))
			}
			saved := selfServiceJournal(t, d, true)
			assertSelfServiceRetry(t, f, 1, 1)
			assertSelfServiceOwnMailboxFlag(t, f, true)
			assertSelfServiceOwnMailboxNoPeerAuthority(t, d)
			d = reopenSelfServiceDaemon(t, d)
			assertSelfServiceRetained(t, saved, d, true)
		})
	}
}

func TestSelfServiceOwnMailboxDaemonInvalidACKNeverEnrolls(t *testing.T) {
	parallelDaemonTest(t)
	for _, tc := range []struct{ name, mailbox string }{
		{"foreign-mailbox", strconv.Quote(selfServiceOwnMailboxID(routingKey(t)[32:]))},
		{"empty-mailbox", `""`},
		{"null-mailbox", `null`},
		{"wrong-mailbox-type", `true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSelfServiceHTTPPeer(t, `"self_service"`)
			f.mu.Lock()
			f.admin = true
			f.badACK = `{"id":"$id","mailbox":` + tc.mailbox + `}`
			f.mu.Unlock()
			d := openSelfServiceDaemon(t, f, nil)
			if err := requestSelfServiceInvite(t, d); safeCode(err) != "invalid_response" {
				t.Fatal("invalid own-mailbox ACK was accepted:", safeCode(err))
			}
			if len(selfServiceJournal(t, d, false).Issued) != 1 {
				t.Fatal("invalid ACK discarded the sole retained publication")
			}
			assertSelfServiceRetry(t, f, 1, 1)
			assertSelfServiceOwnMailboxFlag(t, f, true)
			assertSelfServiceOwnMailboxNoPeerAuthority(t, d)
		})
	}
}

func TestSelfServiceOwnMailboxDaemonReplayAfterHintDisappears(t *testing.T) {
	parallelDaemonTest(t)
	f := newSelfServiceHTTPPeer(t, `"self_service"`)
	f.mu.Lock()
	f.admin, f.dropACK = true, true
	f.mu.Unlock()
	d := openSelfServiceDaemon(t, f, nil)
	if err := requestSelfServiceInvite(t, d); safeCode(err) != "transport_unavailable" {
		t.Fatal("fixture did not lose the publication response:", safeCode(err))
	}
	before := selfServiceJournal(t, d, false)
	assertSelfServiceRetry(t, f, 1, 1)
	d = reopenSelfServiceDaemon(t, d)
	f.mu.Lock()
	f.hint = ""
	// Model the server's exact existing self-service replay, which can verify
	// its previously created own mailbox even after new admission is closed.
	f.badACK = selfServiceOwnMailboxACK(f.key[32:])
	f.mu.Unlock()
	if err := d.Sync(t.Context()); err != nil {
		t.Fatal("retained own-mailbox publication failed recovery:", safeCode(err))
	}
	assertSelfServiceRetained(t, before, d, true)
	assertSelfServiceRetry(t, f, 2, 1)
	assertSelfServiceOwnMailboxFlag(t, f, true)
	assertSelfServiceOwnMailboxNoPeerAuthority(t, d)
	d = reopenSelfServiceDaemon(t, d)
	assertSelfServiceRetained(t, before, d, true)
}

func TestSelfServiceOwnMailboxDaemonNoHintLegacyAdminRemainsCompatible(t *testing.T) {
	parallelDaemonTest(t)
	f := newSelfServiceHTTPPeer(t, "")
	f.mu.Lock()
	f.admin = true
	f.mu.Unlock()
	d := openSelfServiceDaemon(t, f, nil)
	f.mu.Lock()
	f.beforeACK = func() {
		saved, err := loadState(d.options.StateDir, d.sealer)
		if err != nil || !saved.Enrolled {
			t.Error("legacy admin did not save its separate mailbox enrollment first")
		}
	}
	f.mu.Unlock()
	if err := requestSelfServiceInvite(t, d); err != nil {
		t.Fatal("no-hint legacy admin publication stopped working:", safeCode(err))
	}
	trace := f.snapshot()
	if trace.enroll != 1 || trace.publish != 1 || trace.spent != 1 {
		t.Fatal("no-hint admin flow bypassed separate enrollment or repeated publication")
	}
	selfServiceJournal(t, d, true)
	assertSelfServiceOwnMailboxFlag(t, f, false)
	assertSelfServiceOwnMailboxNoPeerAuthority(t, d)
}

func TestSelfServiceOwnMailboxDaemonRequirementEndsOnlyAfterValidatedACK(t *testing.T) {
	parallelDaemonTest(t)
	f := newSelfServiceHTTPPeer(t, `"self_service"`)
	d := openSelfServiceDaemon(t, f, nil)
	if err := requestSelfServiceInvite(t, d); err != nil {
		t.Fatal("initial own-mailbox publication failed:", safeCode(err))
	}
	before := selfServiceJournal(t, d, true)
	assertSelfServiceOwnMailboxFlag(t, f, true)
	d = reopenSelfServiceDaemon(t, d)
	f.mu.Lock()
	f.hint = ""
	// Admission was already acknowledged and saved. An exact replay may now
	// accept a legacy id-only ACK without guessing any new mailbox ownership.
	f.badACK = `{"id":"$id"}`
	f.mu.Unlock()
	if err := d.Sync(t.Context()); err != nil {
		t.Fatal("acknowledged publication replay failed:", safeCode(err))
	}
	assertSelfServiceRetained(t, before, d, true)
	assertSelfServiceRetry(t, f, 2, 1)
	trace := f.snapshot()
	var first, replay relayclient.InviteRequest
	if json.Unmarshal(trace.bodies[0], &first) != nil || json.Unmarshal(trace.bodies[1], &replay) != nil || !first.RequireOwnMailbox || replay.RequireOwnMailbox {
		t.Fatal("transient own-mailbox requirement did not track validated saved enrollment")
	}
	var wire map[string]json.RawMessage
	if json.Unmarshal(trace.bodies[1], &wire) != nil || wire["require_own_mailbox"] != nil {
		t.Fatal("acknowledged replay broke legacy omitempty")
	}
	assertSelfServiceOwnMailboxNoPeerAuthority(t, d)
}

func selfServiceOwnMailboxID(rawPublic []byte) string {
	digest := sha256.Sum256(rawPublic)
	return hex.EncodeToString(digest[:])
}

func selfServiceOwnMailboxACK(rawPublic []byte) string {
	return `{"id":"$id","mailbox":` + strconv.Quote(selfServiceOwnMailboxID(rawPublic)) + `}`
}

func assertSelfServiceOwnMailboxFlag(t *testing.T, f *selfServiceHTTPPeer, required bool) {
	t.Helper()
	trace := f.snapshot()
	if len(trace.bodies) == 0 {
		t.Fatal("own-mailbox fixture never reached a signed publication")
	}
	for _, raw := range trace.bodies {
		var body map[string]json.RawMessage
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal("publication request was not a JSON object")
		}
		value, present := body["require_own_mailbox"]
		if required && (!present || string(value) != "true") || !required && present {
			t.Fatal("signed publication lost the own-mailbox requirement or broke legacy omitempty")
		}
	}
}

func assertSelfServiceOwnMailboxNoPeerAuthority(t *testing.T, d *Daemon) {
	t.Helper()
	grants, err := d.db.ListGrants(t.Context())
	if err != nil || len(grants) != 0 || len(d.state.Peers) != 0 || len(d.state.Joining) != 0 {
		t.Fatal("publication receipt was confused with recipient-issued peer authority")
	}
}
