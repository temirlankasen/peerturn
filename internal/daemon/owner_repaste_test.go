package daemon

import (
	"crypto/tls"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/relayclient"
)

func TestOwnerRepasteDuringFailedJoinRetainsFreshInvitation(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	relay := newRelayFixture(t, admin)
	issuer := openRecoveryDaemon(t, relay, admin)
	d := openRecoveryDaemon(t, relay, routingKey(t))
	created, err := issuer.createInvite(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var invitation struct{ URL string }
	decodeResult(t, created, &invitation)
	capability := provenJoinCapability(t, d)
	ownerPastedInvite(t, d, invitation.URL)
	hash, _, err := invite.OriginHashes(invitation.URL)
	if err != nil {
		t.Fatal(err)
	}
	original := d.joinOrigins.owner[hash]
	transport, ok := relay.server.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatal("fixture requires standard TLS transport")
	}
	transport = transport.Clone()
	transport.TLSClientConfig.VerifyConnection = func(tls.ConnectionState) error {
		d.mu.Lock()
		defer d.mu.Unlock()
		r := d.joinOrigins.owner[hash]
		r.expires = time.Now().Add(-time.Second)
		d.joinOrigins.owner[hash] = r
		// Codex owner boundaries can record a repaste while transport releases mu.
		d.recordOwnerInvites([]string{invitation.URL}, original.epoch)
		return errors.New("fixture transport failure")
	}
	d.relay, err = relayclient.New(relayclient.Options{URL: relay.server.URL, Key: routePrivate(d.state.RoutingSeed), HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Dispatch(t.Context(), pendingJoinRequest(t, d, capability, ""))
	fresh, exists := d.joinOrigins.owner[hash]
	if err == nil || !exists || fresh.handle == original.handle || len(fresh.link) == 0 || len(d.state.Joining) != 0 {
		t.Fatal("failed old join consumed a fresh owner repaste")
	}
}
