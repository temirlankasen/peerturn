package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func openHelper(t *testing.T, d *Daemon) map[string]string {
	t.Helper()
	value, err := d.Dispatch(t.Context(), localipc.Request{Method: "adapter.open", NativeSession: "11111111-1111-4111-8111-111111111111", NativeSocket: "/tmp/claude.sock", RuntimeVersion: "2.1.306", Provider: "gateway"})
	if err != nil {
		t.Fatal(err)
	}
	opened, ok := value.(map[string]string)
	if !ok {
		t.Fatalf("open result %T", value)
	}
	return opened
}

// After a daemon restart the helper must learn that this session was bound
// before, so it may renew the proof itself; a first registration is never
// told so. A link pasted while the proof is still pending is kept, and a
// second challenge in flight reuses the pending nonce.
func TestHelperReopenLearnsPinnedSessionAndKeepsEarlyLinks(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	if first := openHelper(t, d); first["pinned"] != "false" {
		t.Fatalf("fresh daemon reported a pinned session: %v", first)
	}
	one, err := d.Dispatch(t.Context(), localipc.Request{Method: "adapter.challenge", NativeSocket: "/tmp/claude.sock"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := d.Dispatch(t.Context(), localipc.Request{Method: "adapter.challenge", NativeSocket: "/tmp/claude.sock"})
	if err != nil {
		t.Fatal(err)
	}
	first, firstOK := one.(map[string]string)
	second, secondOK := two.(map[string]string)
	if !firstOK || !secondOK || first["nonce"] == "" || first["nonce"] != second["nonce"] {
		t.Fatal("a pending challenge was replaced instead of reused")
	}
	nonce := first["nonce"]
	b := d.claudeBinding
	if _, err = d.Dispatch(t.Context(), localipc.Request{Method: "adapter.confirm", NativeSession: b.NativeSessionID, Instance: b.Instance, Nonce: nonce}); err != nil {
		t.Fatal(err)
	}
	// The restart: the lease is gone, the pin survived in the journal.
	d.claudeBinding = nil
	if again := openHelper(t, d); again["pinned"] != "true" {
		t.Fatalf("restarted daemon hid the pin: %v", again)
	}
	b = d.claudeBinding
	link := syntheticJoinLink("early.invalid", "A")
	if _, err = d.Dispatch(t.Context(), localipc.Request{Method: "adapter.boundary", NativeSession: b.NativeSessionID, NativeSocket: b.Socket, Instance: b.Instance, OwnerInvites: []string{link}}); err != nil {
		t.Fatal(err)
	}
	hash, _, err := invite.OriginHashes(link)
	if err != nil || len(d.joinOrigins.owner[hash].link) == 0 {
		t.Fatal("a link pasted before confirmation was dropped")
	}
	// Until the proof completes the hook is reported unbound, never active.
	if d.hookState() != "unbound" {
		t.Fatalf("hook state before confirmation: %q", d.hookState())
	}
}
