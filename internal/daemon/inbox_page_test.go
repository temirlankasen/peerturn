package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestInboxSurfaceReturnsContinuationWithoutOmissions(t *testing.T) {
	a, b := pairedStores(t)
	aid, bid := peerID(b.state.Chain), peerID(a.state.Chain)
	limits := grant.DefaultLimits()
	limits.MaxBurst = 32
	limits.MaxMessagesPerMinute = 32
	limits.MaxTurnsPerTenMinutes = 32
	now := unixNow()
	signed, err := grant.Issue(grant.Grant{Version: 1, GrantID: randomID(), Issuer: b.cert.Binding, Subject: a.cert.Binding, Scope: grant.Coauthor, GrantEpoch: 1, NotBefore: now, ExpiresAt: now + 3600, Limits: limits, ReplyRoute: &a.cert.Binding}, b.signingKey())
	if err != nil {
		t.Fatal(err)
	}
	a.state.Peers[aid].Outgoing = signed
	b.state.Peers[bid].Incoming = signed
	if err = a.installGrant(t.Context(), signed, b.state.Chain, a.state.Chain); err != nil {
		t.Fatal(err)
	}
	if err = b.installGrant(t.Context(), signed, b.state.Chain, a.state.Chain); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for seq := uint64(1); seq <= 26; seq++ {
		a.state.Sequences[aid] = seq - 1
		packet, prepareErr := a.makePacket(aid, a.state.Peers[aid], protocol.Coauthor, "page", nil)
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		want[packet.Message.MessageID] = true
		if err = b.receive(t.Context(), packet.Packet, seq); err != nil {
			t.Fatal(err)
		}
	}
	cursor := ""
	seen := map[string]bool{}
	for {
		result, callErr := b.Dispatch(t.Context(), localipc.Request{Method: "inbox", Session: b.state.Label, Cursor: cursor})
		if callErr != nil {
			t.Fatal(callErr)
		}
		page, ok := result.(InboxPage)
		if !ok {
			t.Fatal("unstable inbox response")
		}
		if len(page.Messages) > 24 {
			t.Fatal("unbounded inbox page")
		}
		for _, message := range page.Messages {
			if seen[message.MessageID] || !want[message.MessageID] {
				t.Fatal("duplicate or unexpected message")
			}
			seen[message.MessageID] = true
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			t.Fatal("nonadvancing cursor")
		}
		cursor = page.NextCursor
	}
	if len(seen) != len(want) {
		t.Fatal("messages omitted by inbox surface")
	}
}
