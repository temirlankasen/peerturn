package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/relay/config"
)

// A relay from before 0.2.0 refuses links longer than ten minutes. The
// daemon learns that from the refusal, mints a ten-minute link instead and
// says so, rather than failing connect until the relay is updated.
func TestInvitationFallsBackOnTenMinuteRelay(t *testing.T) {
	parallelDaemonTest(t)
	operator := routingKey(t)
	f := newSelfServiceE2ERelayConfigured(t, publicKey(operator[32:]), true, func(c *config.Config) { c.SelfService.InviteLifetimeSeconds = 600 })
	d := openSelfServiceE2EDaemon(t, f, "legacy-relay")
	var created map[string]any
	selfServiceE2ECall(t, d, localipc.Request{Method: "invite", Scope: "coauthor"}, &created)
	expires, ok := created["expires_at"].(float64)
	if !ok || int64(expires)-unixNow() > legacyInviteLifetime || created["relay_max_lifetime"] != float64(legacyInviteLifetime) {
		t.Fatalf("link not shortened for the old relay: %+v", created)
	}
	if d.inviteCap != legacyInviteLifetime {
		t.Fatalf("cap not remembered: %d", d.inviteCap)
	}
	listed := d.listInvites()
	if len(listed) != 1 || listed[0].State != "unused" {
		t.Fatalf("the refused long link must not linger: %+v", listed)
	}
	var again map[string]any
	selfServiceE2ECall(t, d, localipc.Request{Method: "invite", Scope: "coauthor"}, &again)
	if again["url"] == created["url"] || again["relay_max_lifetime"] != float64(legacyInviteLifetime) {
		t.Fatalf("second link ignored the remembered cap: %+v", again)
	}
}

// On a current relay the same request gets the full day.
func TestInvitationGetsFullDayOnCurrentRelay(t *testing.T) {
	parallelDaemonTest(t)
	operator := routingKey(t)
	d := openSelfServiceE2EDaemon(t, newSelfServiceE2ERelay(t, publicKey(operator[32:]), true), "current-relay")
	var created map[string]any
	selfServiceE2ECall(t, d, localipc.Request{Method: "invite", Scope: "coauthor"}, &created)
	expires, ok := created["expires_at"].(float64)
	if !ok || int64(expires)-unixNow() < 23*60*60 || created["relay_max_lifetime"] != nil {
		t.Fatalf("day-long link not issued: %+v", created)
	}
}
