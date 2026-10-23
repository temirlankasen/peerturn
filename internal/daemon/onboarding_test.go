package daemon

import (
	"testing"
)

func TestInviteUsesAdminEnrollmentAndEncryptedDescriptor(t *testing.T) {
	parallelDaemonTest(t)
	key := routingKey(t)
	f := newRelayFixture(t, key)
	dir := stateDir(t)
	if _, err := Initialize(t.Context(), InitOptions{StateDir: dir, RelayURL: f.server.URL, SessionLabel: "explicit", FilePermissions: true, RoutingKey: key}); err != nil {
		t.Fatal(err)
	}
	d, err := Open(t.Context(), Options{StateDir: dir, HTTPClient: f.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := d.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if _, err = d.createInvite(t.Context()); err != nil {
		t.Fatalf("invite error %T: %v", err, err)
	}
}
