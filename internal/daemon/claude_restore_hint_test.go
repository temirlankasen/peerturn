package daemon

import (
	"testing"
)

// After an upgrade from a release that did not remember bound sessions in the
// plugin, setup needs the pinned session to restore its helper; nothing else.
func TestBoundClaudeSessionNamesOnlyAValidPinnedSession(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	a := newDaemonFixture(t, newRelayFixture(t, admin), "a", admin)
	a.stop(t)
	if got := BoundClaudeSession(t.Context(), a.dir); got != "" {
		t.Fatalf("unbound profile named a session: %q", got)
	}
	const pinned = "8324c903-1df0-4c5e-9d7a-0123456789ab"
	rewriteState(t, a, func(s *clientState) { s.NativeSessionID = pinned })
	if got := BoundClaudeSession(t.Context(), a.dir); got != pinned {
		t.Fatalf("pinned session not named: %q", got)
	}
	rewriteState(t, a, func(s *clientState) { s.NativeSessionID = "--config=evil" })
	if got := BoundClaudeSession(t.Context(), a.dir); got != "" {
		t.Fatalf("malformed session ID reached setup: %q", got)
	}
	if got := BoundClaudeSession(t.Context(), t.TempDir()); got != "" {
		t.Fatalf("missing profile named a session: %q", got)
	}
}
