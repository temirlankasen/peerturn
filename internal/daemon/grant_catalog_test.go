package daemon

import (
	"encoding/json"
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

func TestGrantCatalogPeerRevocationAgreesWithStatusAndSendFence(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	id := peerID(b.state.Chain)
	input := incomingData(t, a, b)
	raw, err := codec.Canonical(input.Packet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.OutboxEnqueue(t.Context(), store.OutboxRequest{Packet: raw, Message: input.Message, GrantID: input.Message.GrantID, Now: unixNow()}); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(protocol.ReceiptPayload{MessageID: input.Message.MessageID, AttemptID: input.Message.MessageID, Stage: "revoked", EvidenceKind: "local_revoke", ReasonCode: "revoked", At: unixNow()})
	if err != nil {
		t.Fatal(err)
	}
	control, err := b.makePacket(peerID(a.state.Chain), b.state.Peers[peerID(a.state.Chain)], protocol.Receipt, string(body), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.receive(t.Context(), control.Packet, 1); err != nil {
		t.Fatal(err)
	}
	value, err := a.Dispatch(t.Context(), localipc.Request{Method: "grants", Session: "explicit"})
	if err != nil {
		t.Fatal(err)
	}
	var catalog []struct {
		GrantID          string `json:"grant_id"`
		Revoked          bool   `json:"revoked"`
		EffectiveRevoked bool   `json:"effective_revoked"`
	}
	decodeResult(t, value, &catalog)
	found := false
	for _, entry := range catalog {
		if entry.GrantID == input.Message.GrantID {
			found = true
			if entry.Revoked || !entry.EffectiveRevoked {
				t.Fatalf("remote revoke misreported as local policy: %+v", entry)
			}
		} else if entry.Revoked || entry.EffectiveRevoked {
			t.Fatal("reverse grant was silently revoked")
		}
	}
	if !found || len(a.peers()) != 1 || a.peers()[0].State != "revoked" {
		t.Fatal("catalog disagrees with authenticated peer status")
	}
	if _, err = a.send(t.Context(), localipc.Request{Method: "send", Peer: id, Kind: "coauthor", Body: "later send"}); !fault.IsCode(err, fault.Revoked) {
		t.Fatalf("existing revoke fence changed: %v", err)
	}
	if err = a.db.Expire(t.Context(), unixNow()+90*24*60*60); err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Receipt(t.Context(), input.Message.MessageID); !fault.IsCode(err, store.NotFound) {
		t.Fatalf("expiry did not collect revocation evidence: %v", err)
	}
	assertDaemonCatalogRevoked(t, a, input.Message.GrantID, true)
	incoming, err := a.incomingGrant(a.state.Peers[id])
	if err != nil {
		t.Fatal(err)
	}
	assertDaemonCatalogRevoked(t, a, incoming.GrantID, false)
	if len(a.peers()) != 1 || a.peers()[0].State != "revoked" {
		t.Fatal("GC changed authenticated peer status")
	}
	if _, err = a.send(t.Context(), localipc.Request{Method: "send", Peer: id, Kind: "coauthor", Body: "after GC"}); !fault.IsCode(err, fault.Revoked) {
		t.Fatalf("GC changed the revoke fence: %v", err)
	}
}

func assertDaemonCatalogRevoked(t *testing.T, d *Daemon, id string, want bool) {
	t.Helper()
	value, err := d.Dispatch(t.Context(), localipc.Request{Method: "grants", Session: "explicit"})
	if err != nil {
		t.Fatal(err)
	}
	var items []store.GrantInfo
	decodeResult(t, value, &items)
	for _, item := range items {
		if item.GrantID == id {
			if item.Revoked || item.EffectiveRevoked != want {
				t.Fatalf("catalog lost receipt state: local=%v effective=%v want=%v", item.Revoked, item.EffectiveRevoked, want)
			}
			return
		}
	}
	t.Fatal("catalog lost grant")
}
