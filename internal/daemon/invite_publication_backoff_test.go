package daemon

import (
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestInvitePublicationFailureBackoffPreservesDiagnosticsAndFetch(t *testing.T) {
	parallelDaemonTest(t)
	f := newSelfServiceHTTPPeer(t, `"self_service"`)
	d := openSelfServiceDaemon(t, f, nil)
	if err := requestSelfServiceInvite(t, d); err != nil {
		t.Fatal("initial healthy publication failed:", safeCode(err))
	}
	f.mu.Lock()
	f.status, f.code = 429, "quota"
	f.mu.Unlock()
	if err := requestSelfServiceInvite(t, d); safeCode(err) != "quota" {
		t.Fatal("fixture did not refuse the unrelated publication:", safeCode(err))
	}
	var failed *issuedInvite
	for _, entry := range d.state.Issued {
		if entry.Failure == "quota" {
			failed = entry
		}
	}
	if failed == nil || failed.publication.delay != 2*time.Second {
		t.Fatal("first failed publication did not establish bounded backoff")
	}
	// Control eligibility directly, without sleeping or removing failure state.
	gate := time.Now().Add(time.Minute)
	failed.publication.retryAt = gate
	for range 8 {
		if err := d.Sync(t.Context()); err != nil {
			t.Fatal("deferred publication blocked Fetch:", safeCode(err))
		}
		value, err := d.Dispatch(t.Context(), localipc.Request{Method: "status", Session: d.state.Label})
		status, ok := value.(Status)
		if err != nil || !ok || len(status.OnboardingErrors) != 1 || status.OnboardingErrors[0] != "quota" {
			t.Fatal("deferred publication cleared truthful public diagnostics")
		}
	}
	trace := f.snapshot()
	if trace.publish != 2 || trace.poll != 8 || trace.spent != 1 || failed.publication.retryAt != gate {
		t.Fatal("backoff repeated publication, delayed its gate, or starved Fetch")
	}
	saved := selfServiceJournal(t, d, true)
	if saved.Issued[failed.Descriptor.InviteID].Failure != "quota" {
		t.Fatal("deferred failure disappeared from the sealed journal")
	}
	for range 8 {
		failed.publication.retryAt = time.Time{}
		if err := d.Sync(t.Context()); err != nil {
			t.Fatal("due quota failure blocked Fetch:", safeCode(err))
		}
		if failed.publication.delay < 2*time.Second || failed.publication.delay > 2*time.Minute || failed.Failure != "quota" {
			t.Fatal("publication retry escaped bounded backoff or lost diagnostics")
		}
	}
	if failed.publication.delay != 2*time.Minute || f.snapshot().poll != 16 {
		t.Fatal("backoff did not saturate independently of continuing Fetch")
	}
	f.mu.Lock()
	f.status, f.code = 0, ""
	f.mu.Unlock()
	failed.publication.retryAt = time.Time{}
	if err := d.Sync(t.Context()); err != nil || failed.Failure != "" || !failed.publication.confirmed || failed.publication.delay != 0 {
		t.Fatal("successful exact retry did not clear its confirmed failure state")
	}
	if f.snapshot().spent != 2 || len(d.state.Issued) != 2 {
		t.Fatal("failure recovery allocated another invitation")
	}
}
