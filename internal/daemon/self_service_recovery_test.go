package daemon

import (
	"bytes"
	"reflect"
	"testing"
)

func assertSelfServiceRetry(t *testing.T, f *selfServiceHTTPPeer, requests, spends int) {
	t.Helper()
	trace := f.snapshot()
	if trace.enroll != 0 || trace.publish != requests || trace.spent != spends || len(trace.bodies) != requests {
		t.Fatal("recovery enrolled separately, retried unexpectedly, or allocated another publication")
	}
	for n := 1; n < requests; n++ {
		first, firstErr := selfServicePublicationMaterial(trace.bodies[0])
		retry, retryErr := selfServicePublicationMaterial(trace.bodies[n])
		if firstErr != nil || retryErr != nil {
			t.Fatal("invalid retained publication body")
		}
		if !bytes.Equal(first, retry) || trace.signatures[n-1] == trace.signatures[n] {
			t.Fatal("recovery changed ID/packet/expiry/voucher or reused a signed nonce")
		}
	}
}

func assertSelfServiceRetained(t *testing.T, before clientState, d *Daemon, enrolled bool) {
	t.Helper()
	after := selfServiceJournal(t, d, enrolled)
	if len(before.Issued) != 1 || len(after.Issued) != 1 {
		t.Fatal("recovery must retain exactly one existing invitation")
	}
	for id, old := range before.Issued {
		entry := after.Issued[id]
		if entry == nil || entry.URL != old.URL || !reflect.DeepEqual(entry.Packet, old.Packet) || !reflect.DeepEqual(entry.Descriptor, old.Descriptor) {
			t.Fatal("restart replaced existing sealed invitation retry material")
		}
	}
}

func TestSelfServiceDaemonQuotaRetrySurvivesRestartWithoutNewInvitation(t *testing.T) {
	parallelDaemonTest(t)
	f := newSelfServiceHTTPPeer(t, `"self_service"`)
	f.mu.Lock()
	f.status, f.code = 429, "quota"
	f.mu.Unlock()
	d := openSelfServiceDaemon(t, f, nil)
	if err := requestSelfServiceInvite(t, d); safeCode(err) != "quota" {
		t.Fatal("quota refusal was hidden or happened at pre-enrollment", err)
	}
	before := selfServiceJournal(t, d, false)
	assertSelfServiceRetry(t, f, 1, 0)
	d = reopenSelfServiceDaemon(t, d)
	assertSelfServiceRetained(t, before, d, false)
	assertSelfServiceRetry(t, f, 1, 0)
	f.mu.Lock()
	f.status, f.code = 0, ""
	f.mu.Unlock()
	if err := d.Sync(t.Context()); err != nil {
		t.Fatal("saved publication did not recover when capacity returned", err)
	}
	assertSelfServiceRetained(t, before, d, true)
	assertSelfServiceRetry(t, f, 2, 1)
	if err := d.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertSelfServiceRetry(t, f, f.snapshot().publish, 1)
}

func TestSelfServiceDaemonMalformedACKDoesNotInferEnrollment(t *testing.T) {
	parallelDaemonTest(t)
	for _, ack := range []string{`{}`, `{"id":"wrong-slot"}`, `{"id":1}`, `{"id":"$id","unknown":true}`, `{"id":"$id","id":"$id"}`, `{`} {
		t.Run(ack, func(t *testing.T) {
			f := newSelfServiceHTTPPeer(t, `"self_service"`)
			f.mu.Lock()
			f.badACK = ack
			f.mu.Unlock()
			d := openSelfServiceDaemon(t, f, nil)
			if err := requestSelfServiceInvite(t, d); safeCode(err) != "invalid_response" {
				t.Fatal("invalid publication ACK was treated as enrollment or never reached publication", err)
			}
			before := selfServiceJournal(t, d, false)
			assertSelfServiceRetry(t, f, 1, 1)
			d = reopenSelfServiceDaemon(t, d)
			assertSelfServiceRetained(t, before, d, false)
			f.mu.Lock()
			f.badACK = ""
			f.mu.Unlock()
			if err := d.Sync(t.Context()); err != nil {
				t.Fatal("exact pending publication failed recovery", err)
			}
			assertSelfServiceRetained(t, before, d, true)
			assertSelfServiceRetry(t, f, 2, 1)
		})
	}
}

func TestSelfServiceDaemonLostPublicationResponseNeverGuessesEnrollment(t *testing.T) {
	parallelDaemonTest(t)
	f := newSelfServiceHTTPPeer(t, `"self_service"`)
	f.mu.Lock()
	f.dropACK = true
	f.mu.Unlock()
	d := openSelfServiceDaemon(t, f, nil)
	if err := requestSelfServiceInvite(t, d); safeCode(err) != "transport_unavailable" {
		t.Fatal("fixture did not lose a publication response after its remote commit", err)
	}
	before := selfServiceJournal(t, d, false)
	assertSelfServiceRetry(t, f, 1, 1)
	d = reopenSelfServiceDaemon(t, d)
	assertSelfServiceRetained(t, before, d, false)
	assertSelfServiceRetry(t, f, 1, 1)
	f.mu.Lock()
	f.status, f.code = 503, "unavailable"
	f.mu.Unlock()
	if err := d.Sync(t.Context()); err != nil && safeCode(err) != "unavailable" {
		t.Fatal(err)
	}
	assertSelfServiceRetained(t, before, d, false)
	assertSelfServiceRetry(t, f, 2, 1)
	d = reopenSelfServiceDaemon(t, d)
	assertSelfServiceRetained(t, before, d, false)
	f.mu.Lock()
	f.status, f.code = 0, ""
	f.mu.Unlock()
	if err := d.Sync(t.Context()); err != nil {
		t.Fatal("identical publication failed after response loss", err)
	}
	assertSelfServiceRetained(t, before, d, true)
	assertSelfServiceRetry(t, f, 3, 1)
	d = reopenSelfServiceDaemon(t, d)
	assertSelfServiceRetained(t, before, d, true)
}
