package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

func TestPendingAskWithFullControlLaneDoesNotBlockFlushOrLive(t *testing.T) {
	a, b, runtime := nativePair(t)
	id := peerID(b.state.Chain)
	live := incomingData(t, a, b)
	if err := b.receive(t.Context(), live.Packet, 100); err != nil {
		t.Fatal(err)
	}
	a.state.Sequences[id]++
	ask, err := a.makePacket(id, a.state.Peers[id], protocol.Ask, "pending unavailable ask", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := codec.Canonical(ask.Packet)
	if err != nil {
		t.Fatal(err)
	}
	peer := b.state.Peers[peerID(a.state.Chain)]
	g, err := b.incomingGrant(peer)
	if err != nil {
		t.Fatal(err)
	}
	// Seed a durable pending ask as after a restart before responder preflight.
	var storage reservedPacket
	_, err = b.db.InboxCommit(t.Context(), store.InboxRequest{Packet: raw, Mailbox: b.mailbox(), MailboxSeq: 101, GrantID: g.GrantID, Open: b.openOptions(peer), Receipt: func(r protocol.ReceiptPayload) ([]byte, error) {
		body, encodeErr := codec.Canonical(r)
		if encodeErr != nil {
			return nil, encodeErr
		}
		var prepareErr error
		storage, prepareErr = b.makePacket(peerID(a.state.Chain), peer, protocol.Receipt, string(body), nil)
		if prepareErr != nil {
			return nil, prepareErr
		}
		return codec.Canonical(storage.Packet)
	}})
	if err != nil {
		t.Fatal(err)
	}
	b.state.Sequences[storage.PeerID]++
	b.state.Pending[storage.Message.MessageID] = outboundOrder{PeerID: storage.PeerID, SenderSeq: storage.Message.SenderSeq}
	// receiver_stored uses the incoming grant's control lane; native terminal
	// notices use the reciprocal grant. Fill the latter to its actual ceiling.
	for range store.MaxControlMessages {
		r := protocol.ReceiptPayload{MessageID: ask.Message.MessageID, AttemptID: ask.Message.MessageID, Stage: "receiver_stored", EvidenceKind: "local_commit", At: unixNow()}
		if err = b.queueNativeReceipt(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	before := len(b.state.Pending)
	select {
	case <-b.wake:
	default:
	}
	if err = b.rejectUnavailableResponders(t.Context()); err != nil {
		t.Fatal("full control lane blocked other admissions", err)
	}
	select {
	case <-b.wake:
		t.Fatal("failed rejection creates an unbounded wake loop")
	default:
	}
	// A healthy relay must be allowed to drain old controls before the pending
	// ask tries to reserve another control, and live input must keep progressing.
	if err = b.Sync(t.Context()); err != nil {
		t.Fatal("pending ask wedged sync before control flush", err)
	}
	if len(runtime.frames) != 1 || len(b.state.Pending) >= before {
		t.Fatal("pending ask starved flush or live delivery")
	}
	r, err := b.db.Receipt(t.Context(), ask.Message.MessageID)
	if err != nil || r.Stage != "rejected" || r.Outcome != "responder_unavailable" {
		t.Fatal("ask was not settled after queue drain", r, err)
	}
}
