package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestConnectExpectedProfileRejectsReplacementDaemon(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	relay := newRelayFixture(t, admin)
	first := openRecoveryDaemon(t, relay, admin)
	second := openRecoveryDaemon(t, relay, routingKey(t))
	_, err := second.Dispatch(t.Context(), localipc.Request{Method: "status", ProfileID: ProfileID(first.options.StateDir, first.state.RelayURL)})
	if err == nil || localipc.PublicError(err).Code != "conflict" {
		t.Fatalf("replacement daemon accepted another profile's request: %v", err)
	}
}

func TestConnectInvitationRejectsReplacedEpochBeforeEffects(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	relay := newRelayFixture(t, admin)
	d := openRecoveryDaemon(t, relay, admin)
	bindClaude(t, d)
	epoch := d.claudeBinding.MCPEpoch()
	if _, err := d.Dispatch(t.Context(), localipc.Request{Method: "session.unbind"}); err != nil {
		t.Fatal(err)
	}
	bindClaude(t, d)
	_, err := d.Dispatch(t.Context(), localipc.Request{Method: "invite", Session: d.state.Label, Scope: "coauthor", BindingEpoch: epoch})
	if err == nil || localipc.PublicError(err).Code != "needs_rebind" || len(d.state.Issued) != 0 || d.state.Enrolled {
		t.Fatalf("stale epoch reached invitation effects: err=%v issued=%d enrolled=%v", err, len(d.state.Issued), d.state.Enrolled)
	}
}
