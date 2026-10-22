package daemon

import (
	"crypto/ed25519"
	"testing"

	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func thirdNativePeer(t *testing.T, b *Daemon) *Daemon {
	t.Helper()
	thread := "019edaa1-3333-7333-8333-333333333333"
	runtime := &nativeFixture{thread: thread}
	o := InitOptions{StateDir: stateDir(t), RelayURL: b.state.RelayURL, SessionLabel: thread, FilePermissions: true, CodexThreadID: thread, Native: runtime}
	if _, err := Initialize(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	c, err := Open(t.Context(), Options{StateDir: o.StateDir, HTTPClient: b.options.HTTPClient, Native: runtime})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := c.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	invite, err := b.createInvite(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	fields, ok := invite.(map[string]any)
	if !ok {
		t.Fatal("invite response type")
	}
	url, ok := fields["url"].(string)
	if !ok {
		t.Fatal("invite URL type")
	}
	if _, err = c.join(t.Context(), url); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if err = b.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err = c.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func TestReviewExpiredPeerAndOutgoingRevokeCannotStarveOtherPeer(t *testing.T) {
	b, a, _ := nativePair(t)
	runtime, ok := b.native.(*nativeFixture)
	if !ok {
		t.Fatal("native fixture type")
	}
	c := thirdNativePeer(t, b)
	for _, sender := range []*Daemon{a, c} {
		for range 5 {
			nativeReceipt(t, sender, localipc.Request{Method: "send", Peer: sender.peers()[0].ID, Kind: "inform", Body: "private body"})
		}
	}
	if err := b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	expired := a.cert
	expired.NotBefore, expired.ExpiresAt = unixNow()-120, unixNow()-1
	cert, err := identity.IssueSession(expired, ed25519.NewKeyFromSeed(a.state.DeviceSeed))
	if err != nil {
		t.Fatal(err)
	}
	p := b.state.Peers[peerID(a.state.Chain)]
	p.Chain.Session, p.Revoked = cert, true
	if _, err = identity.VerifyChain(p.Chain, identity.RootFingerprint(p.Chain.RootPublicKey), unixNow()); err == nil {
		t.Fatal("expiry fixture is still valid")
	}
	offer := nativeValue[map[string]string](t, b, localipc.Request{Method: "codex_inform", MessageID: "owner"})
	if offer["count"] != "8" {
		t.Fatalf("per-prompt work was not capped: %v", offer)
	}
	nativeCall(t, b, localipc.Request{Method: "codex_inform_ack", MessageID: "owner", Cursor: offer["offer_token"]})
	if count, countErr := b.db.NativePendingCount(t.Context()); countErr != nil || count != 2 {
		t.Fatalf("notice consumed wrong set: %d %v", count, countErr)
	}
	page := nativeValue[InboxPage](t, b, localipc.Request{Method: "inbox"})
	if len(page.Messages) != 10 || len(runtime.ids) != 0 {
		t.Fatal("poison hid bodies or inform woke a turn")
	}
}
