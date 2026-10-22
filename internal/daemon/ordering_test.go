package daemon

import (
	"crypto/ed25519"
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relayclient"
	"github.com/temirlankasen/peerturn/internal/store"
)

func TestOfflineBacklogBeyondReplayWindowKeepsChannelOrder(t *testing.T) {
	a, b := pairedStores(t)
	f := newRelayFixture(t, ed25519.NewKeyFromSeed(a.state.RoutingSeed))
	var err error
	a.relay, err = relayclient.New(relayclient.Options{URL: f.server.URL, Key: ed25519.NewKeyFromSeed(a.state.RoutingSeed), HTTPClient: f.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	b.relay, err = relayclient.New(relayclient.Options{URL: f.server.URL, Key: ed25519.NewKeyFromSeed(b.state.RoutingSeed), HTTPClient: f.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.relay.EnrollMailbox(t.Context(), relayclient.MailboxRequest{ID: a.mailbox(), ReadKey: publicKey(a.routingPublic())}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.relay.EnrollMailbox(t.Context(), relayclient.MailboxRequest{ID: b.mailbox(), ReadKey: publicKey(b.routingPublic())}); err != nil {
		t.Fatal(err)
	}
	aid, bid := peerID(b.state.Chain), peerID(a.state.Chain)
	if err = a.allowPeer(t.Context(), a.state.Peers[aid]); err != nil {
		t.Fatal(err)
	}
	if err = b.allowPeer(t.Context(), b.state.Peers[bid]); err != nil {
		t.Fatal(err)
	}
	a.state.Enrolled = true
	b.state.Enrolled = true
	for range 96 {
		r, prepareErr := a.makePacket(aid, a.state.Peers[aid], protocol.Coauthor, "offline", nil)
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		a.state.Reserved = &r
		a.state.Sequences[aid]++
		if err = a.save(); err != nil {
			t.Fatal(err)
		}
		if err = a.recoverReserved(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for len(a.state.Pending) > 0 {
		if err = a.flush(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err = b.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	// Recipient rate rejection still consumes each authenticated sequence in order.
	// A successor must see a valid budget decision, never a lost/future sequence.
	next, err := a.makePacket(aid, a.state.Peers[aid], protocol.Coauthor, "successor", nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Message.SenderSeq != 97 {
		t.Fatal("sender sequence changed")
	}
	if err = b.receive(t.Context(), next.Packet, 97); err != nil {
		t.Fatal(err)
	}
	statuses, err := b.db.Rejections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].Reason != fault.BudgetExceeded {
		t.Fatalf("out-of-order backlog: %+v", statuses)
	}
	// Real TLS plus fsync can cross a token refill boundary. Either admission or
	// the exact authenticated budget refusal is valid; replay/gap errors are not.
	item, readErr := b.db.Inbox(t.Context(), next.Message.MessageID)
	if readErr == nil {
		if item.State != "pending" || string(item.Body) != "successor" {
			t.Fatalf("successor not admitted: %+v", item)
		}
	} else if !fault.IsCode(readErr, store.NotFound) || statuses[0].MessageID != next.Message.MessageID {
		t.Fatalf("successor lost without a budget decision: %v %+v", readErr, statuses)
	}
	frontier, err := b.db.AckFrontier(t.Context(), b.mailbox())
	if err != nil || frontier != 97 {
		t.Fatalf("successor custody gap: %d %v", frontier, err)
	}
	if len(a.state.Pending) != 0 {
		t.Fatal("relayed retry metadata retained")
	}
	// The successor remains a canonical packet across both valid budget outcomes.
	raw, err := codec.Canonical(next.Packet)
	if err != nil || len(raw) == 0 {
		t.Fatal("invalid successor")
	}
	if _, err = b.Dispatch(t.Context(), localipc.Request{Method: "status"}); err != nil {
		t.Fatal(err)
	}
}
