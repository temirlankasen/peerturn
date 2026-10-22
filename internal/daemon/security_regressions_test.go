package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relayclient"
)

func TestRevokeSurvivesExpiredReturnGrant(t *testing.T) {
	a, b := pairedStores(t)
	aid, bid := peerID(b.state.Chain), peerID(a.state.Chain)
	ap, bp := a.state.Peers[aid], b.state.Peers[bid]
	now := unixNow()
	expires := now + 2
	short, err := grant.Issue(grant.Grant{Version: 1, GrantID: randomID(), Issuer: b.cert.Binding, Subject: a.cert.Binding, Scope: grant.Coauthor, GrantEpoch: 1, NotBefore: now, ExpiresAt: expires, Limits: grant.DefaultLimits(), ReplyRoute: &a.cert.Binding}, b.signingKey())
	if err != nil {
		t.Fatal(err)
	}
	ap.Outgoing, bp.Incoming = short, short
	if err = a.installGrant(t.Context(), short, b.state.Chain, a.state.Chain); err != nil {
		t.Fatal(err)
	}
	if err = b.installGrant(t.Context(), short, b.state.Chain, a.state.Chain); err != nil {
		t.Fatal(err)
	}
	incoming, err := b.makePacket(bid, bp, protocol.Coauthor, "pending", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.receive(t.Context(), incoming.Packet, 1); err != nil {
		t.Fatal(err)
	}
	target, err := a.incomingGrant(ap)
	if err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(time.Until(time.Unix(expires, 0)))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal("test canceled before return grant expiry")
	}
	if _, _, expiredErr := a.outgoing(ap); !fault.IsCode(expiredErr, fault.Expired) {
		t.Fatalf("return grant remains usable: %v", expiredErr)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result, err := a.revoke(ctx, target.GrantID)
	if err != nil {
		t.Fatalf("owner revoke blocked by expired peer-issued return grant: %v", err)
	}
	var outcome struct {
		State                    string `json:"state"`
		NotificationsUnavailable bool   `json:"notifications_unavailable"`
	}
	decodeResult(t, result, &outcome)
	if outcome.State != "revoked" || !outcome.NotificationsUnavailable {
		t.Fatalf("notification failure hidden: %+v", outcome)
	}
	entries, err := a.db.ListGrants(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.GrantID == target.GrantID {
			found = true
			if !entry.Revoked {
				t.Fatal("incoming grant remains active")
			}
		}
	}
	if !found {
		t.Fatal("target grant missing")
	}
	receipt, err := a.db.Receipt(t.Context(), incoming.Message.MessageID)
	if err != nil || receipt.Stage != "revoked" {
		t.Fatalf("missing local revocation receipt: %+v %v", receipt, err)
	}
}

func TestPreviewRejectsRemoteNonCoauthor(t *testing.T) {
	for _, kind := range []protocol.Kind{protocol.Ask, protocol.Inform} {
		t.Run(string(kind), func(t *testing.T) {
			a, b := pairedStores(t)
			id := peerID(a.state.Chain)
			packet, err := b.makePacket(id, b.state.Peers[id], kind, "disabled remote scope", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.receive(t.Context(), packet.Packet, 1); err != nil {
				t.Fatal(err)
			}
			rejections, err := a.db.Rejections(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(rejections) != 1 || rejections[0].MessageID != packet.Message.MessageID || rejections[0].Reason != fault.ScopeDenied || rejections[0].Count != 1 {
				t.Fatalf("disabled %s lacks bounded terminal rejection evidence: %+v", kind, rejections)
			}
			frontier, err := a.db.AckFrontier(t.Context(), a.mailbox())
			if err != nil || frontier != 1 {
				t.Fatalf("rejected transport slot not committed: %d %v", frontier, err)
			}
			inbox, err := a.db.ListInbox(t.Context(), 10)
			if err != nil || len(inbox) != 0 {
				t.Fatalf("disabled remote message reached inbox: %+v %v", inbox, err)
			}
			b.state.Sequences[id]++
			accepted, err := b.makePacket(id, b.state.Peers[id], protocol.Coauthor, "allowed next message", nil)
			if err != nil {
				t.Fatal(err)
			}
			if accepted.Message.SenderSeq != 2 {
				t.Fatal("fixture did not advance sender sequence")
			}
			if err = a.receive(t.Context(), accepted.Packet, 2); err != nil {
				t.Fatal(err)
			}
			receipt, err := a.db.Receipt(t.Context(), accepted.Message.MessageID)
			if err != nil || receipt.Stage != "receiver_stored" {
				t.Fatalf("rejected sender sequence blocked subsequent coauthor: %+v %v", receipt, err)
			}
			inbox, err = a.db.ListInbox(t.Context(), 10)
			if err != nil || len(inbox) != 1 || inbox[0].MessageID != accepted.Message.MessageID {
				t.Fatalf("disabled body reached inbox: %+v %v", inbox, err)
			}
		})
	}
}

func TestOwnerDaemonControlsAcceptBlankSession(t *testing.T) {
	d, _ := pairedStores(t)
	f := newRelayFixture(t, routingKey(t))
	client, err := relayclient.New(relayclient.Options{URL: f.server.URL, Key: routingKey(t), HTTPClient: f.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	d.relay = client
	for _, method := range []string{"status", "doctor", "stop"} {
		if _, callErr := d.Dispatch(t.Context(), localipc.Request{Method: method}); callErr != nil {
			t.Fatalf("owner %s needs an unnecessary session label: %v", method, callErr)
		}
		if _, callErr := d.Dispatch(t.Context(), localipc.Request{Method: method, Session: "another-session"}); localipc.ExitCode(callErr) != 3 {
			t.Fatalf("owner %s accepted a mismatched explicit binding: %v", method, callErr)
		}
	}
	for _, method := range []string{"hello", "peers", "send", "invite", "grants", "revoke"} {
		if _, callErr := d.Dispatch(t.Context(), localipc.Request{Method: method}); localipc.ExitCode(callErr) != 3 {
			t.Fatalf("blank binding authorized %s: %v", method, callErr)
		}
	}
}

func TestMCPCapabilityCannotUseOwnerControls(t *testing.T) {
	d, _ := pairedStores(t)
	result, err := d.Dispatch(t.Context(), localipc.Request{Method: "hello", Session: d.state.Label})
	if err != nil {
		t.Fatal(err)
	}
	var hello struct {
		Capability string `json:"capability"`
	}
	decodeResult(t, result, &hello)
	if hello.Capability == "" {
		t.Fatal("no session-bound capability")
	}
	if _, err = d.Dispatch(t.Context(), localipc.Request{Method: "status", Session: d.state.Label, Capability: hello.Capability}); err != nil {
		t.Fatalf("valid capability rejected: %v", err)
	}
	for _, method := range []string{"hello", "doctor", "stop", "invite", "join", "grants", "revoke"} {
		_, callErr := d.Dispatch(t.Context(), localipc.Request{Method: method, Session: d.state.Label, Capability: hello.Capability, Scope: "coauthor"})
		if !fault.IsCode(callErr, fault.ScopeDenied) {
			t.Fatalf("MCP capability reached owner control %s: %v", method, callErr)
		}
	}
	for _, request := range []localipc.Request{
		{Method: "status", Capability: hello.Capability},
		{Method: "status", Session: "another-session", Capability: hello.Capability},
		{Method: "status", Session: d.state.Label, Capability: "unknown-capability"},
	} {
		if _, callErr := d.Dispatch(t.Context(), request); localipc.ExitCode(callErr) != 3 {
			t.Fatalf("capability escaped session binding: %v", callErr)
		}
	}
}

func decodeResult(t *testing.T, value, target any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, target); err != nil {
		t.Fatal(err)
	}
}
