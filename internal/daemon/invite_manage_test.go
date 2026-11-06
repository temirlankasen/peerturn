package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func invitation(t *testing.T, d *Daemon, r localipc.Request) map[string]any {
	t.Helper()
	r.Method, r.Scope = "invite", "coauthor"
	if r.Session == "" {
		r.Session = d.state.Label
	}
	value, err := d.Dispatch(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	created, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("invitation result %T", value)
	}
	return created
}

// Each connect mints a link so two colleagues never share one; --reuse
// prints the latest unused link again, revoke ends one, and a lifetime
// outside the bounds is refused with the rule.
func TestInvitationIsReusedUntilRevokedOrReplaced(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	d := openRecoveryDaemon(t, newRelayFixture(t, admin), admin)
	first := invitation(t, d, localipc.Request{})
	if first["reused"] != false || first["url"] == "" {
		t.Fatalf("first invitation %+v", first)
	}
	expires, ok := first["expires_at"].(int64)
	if !ok || expires-unixNow() < invite.MaxLifetime-60 || expires-unixNow() > invite.MaxLifetime {
		t.Fatalf("default lifetime is not a day: %v", first["expires_at"])
	}
	second := invitation(t, d, localipc.Request{})
	if second["reused"] != false || second["url"] == first["url"] {
		t.Fatalf("a second colleague would get the first link: %+v", second)
	}
	// Both links were minted in the same second; --reuse returns one of them, and the same one again.
	again := invitation(t, d, localipc.Request{Reuse: true})
	if again["reused"] != true || (again["url"] != second["url"] && again["url"] != first["url"]) {
		t.Fatalf("--reuse did not print an unused link: %+v", again)
	}
	if repeat := invitation(t, d, localipc.Request{Reuse: true}); repeat["url"] != again["url"] {
		t.Fatalf("--reuse is not stable: %v then %v", again["url"], repeat["url"])
	}
	short := invitation(t, d, localipc.Request{Reuse: true, Lifetime: 600})
	if short["reused"] != false || short["url"] == second["url"] || short["url"] == first["url"] {
		t.Fatalf("a day-long link was reused for --lifetime 10m: %+v", short)
	}
	if got, hasExpiry := short["expires_at"].(int64); !hasExpiry || got-unixNow() > 600 {
		t.Fatalf("--lifetime 10m ignored: %v", short["expires_at"])
	}
	if again = invitation(t, d, localipc.Request{Reuse: true, Lifetime: 600}); again["reused"] != true || again["url"] != short["url"] {
		t.Fatalf("the ten-minute link was not reused for the same request: %+v", again)
	}
	listed := d.listInvites()
	if len(listed) != 3 || listed[0].State != "unused" {
		t.Fatalf("list %+v", listed)
	}
	for _, bad := range []int64{30, invite.MaxLifetime + 1} {
		if _, err := d.Dispatch(t.Context(), localipc.Request{Method: "invite", Scope: "coauthor", Session: d.state.Label, Lifetime: bad}); localipc.PublicError(err).Code != "invalid_arguments" {
			t.Fatalf("lifetime %d accepted: %v", bad, err)
		}
	}
	id, ok := first["id"].(string)
	if !ok || id == "" {
		t.Fatalf("invitation without an id: %+v", first)
	}
	if _, err := d.Dispatch(t.Context(), localipc.Request{Method: "invite.revoke", Session: d.state.Label, InviteID: id}); err != nil {
		t.Fatal(err)
	}
	if listed = d.listInvites(); len(listed) != 2 || listed[0].ID == id || listed[1].ID == id {
		t.Fatalf("revoked link still listed: %+v", listed)
	}
	if _, err := d.Dispatch(t.Context(), localipc.Request{Method: "invite.revoke", Session: d.state.Label, InviteID: "unknown"}); localipc.PublicError(err).Code != "not_found" {
		t.Fatalf("unknown revoke: %v", err)
	}
	// A reuse request never hands out a link with less than an hour left.
	for _, left := range listed {
		if _, err := d.Dispatch(t.Context(), localipc.Request{Method: "invite.revoke", Session: d.state.Label, InviteID: left.ID}); err != nil && left.ID != id {
			t.Fatal(err)
		}
	}
	next := invitation(t, d, localipc.Request{Reuse: true})
	if next["reused"] != false {
		t.Fatalf("a revoked link was reused: %+v", next)
	}
}
