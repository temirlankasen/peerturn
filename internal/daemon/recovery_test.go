package daemon

import (
	"context"
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

func pairedStores(t *testing.T) (*Daemon, *Daemon) {
	t.Helper()
	aDir, bDir := stateDir(t), stateDir(t)
	for _, dir := range []string{aDir, bDir} {
		if _, err := Initialize(t.Context(), InitOptions{StateDir: dir, RelayURL: "https://relay.example", SessionLabel: "explicit", FilePermissions: true}); err != nil {
			t.Fatal(err)
		}
	}
	a, err := Open(t.Context(), Options{StateDir: aDir})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(t.Context(), Options{StateDir: bDir})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := a.issueGrant(b.cert)
	if err != nil {
		t.Fatal(err)
	}
	bg, err := b.issueGrant(a.cert)
	if err != nil {
		t.Fatal(err)
	}
	ap := &peerState{Chain: b.state.Chain, RoutingKey: b.routingPublic(), Incoming: ag, Outgoing: bg, HelloReceived: true, HelloSent: true}
	bp := &peerState{Chain: a.state.Chain, RoutingKey: a.routingPublic(), Incoming: bg, Outgoing: ag, HelloReceived: true, HelloSent: true}
	a.state.Peers[peerID(b.state.Chain)] = ap
	b.state.Peers[peerID(a.state.Chain)] = bp
	for _, d := range []*Daemon{a, b} {
		for _, p := range d.state.Peers {
			if err = d.installGrant(t.Context(), p.Incoming, d.state.Chain, p.Chain); err != nil {
				t.Fatal(err)
			}
			if err = d.installGrant(t.Context(), p.Outgoing, p.Chain, d.state.Chain); err != nil {
				t.Fatal(err)
			}
		}
		if err = d.save(); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	return a, b
}

func TestReservedPacketRecoveryBeforeAndAfterQueueCommit(t *testing.T) {
	parallelDaemonTest(t)
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[commit], func(t *testing.T) {
			a, b := pairedStores(t)
			id := peerID(b.state.Chain)
			p := a.state.Peers[id]
			r, err := a.makePacket(id, p, protocol.Coauthor, "literal", nil)
			if err != nil {
				t.Fatal(err)
			}
			a.state.Sequences[id]++
			a.state.Reserved = &r
			if err = a.save(); err != nil {
				t.Fatal(err)
			}
			raw, err := codec.Canonical(r.Packet)
			if err != nil {
				t.Fatal(err)
			}
			if commit {
				if _, err = a.db.OutboxEnqueue(t.Context(), store.OutboxRequest{Packet: raw, Message: r.Message, GrantID: r.Message.GrantID, Now: unixNow()}); err != nil {
					t.Fatal(err)
				}
			}
			// Reload the synced journal, as a fresh process would after a crash.
			a.state, err = loadState(a.options.StateDir, a.sealer)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.recoverReserved(t.Context()); err != nil {
				t.Fatal(err)
			}
			retained, err := a.db.OutboxPacket(t.Context(), r.Message.MessageID)
			if err != nil || !equalBytes(retained, raw) {
				t.Fatal("immutable queue retry changed")
			}
			next, err := a.makePacket(id, p, protocol.Coauthor, "next", nil)
			if err != nil || next.Message.SenderSeq != 2 {
				t.Fatal("sender sequence lost continuity")
			}
		})
	}
}

func TestReceiptJournalRecoveryAfterCustodyCommit(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	aid := peerID(b.state.Chain)
	bid := peerID(a.state.Chain)
	input, err := a.makePacket(aid, a.state.Peers[aid], protocol.Coauthor, "once", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := codec.Canonical(input.Packet)
	if err != nil {
		t.Fatal(err)
	}
	payload := protocol.ReceiptPayload{MessageID: input.Message.MessageID, AttemptID: input.Message.MessageID, Stage: "receiver_stored", EvidenceKind: "local_commit", At: unixNow()}
	body, err := codec.Canonical(payload)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := b.makePacket(bid, b.state.Peers[bid], protocol.Receipt, string(body), nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Inbound = &input.Packet
	receipt.MailboxSeq = 1
	b.state.Reserved = &receipt
	b.state.Sequences[bid]++
	if err = b.save(); err != nil {
		t.Fatal(err)
	}
	wire, err := codec.Canonical(receipt.Packet)
	if err != nil {
		t.Fatal(err)
	}
	g, err := b.incomingGrant(b.state.Peers[bid])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.db.InboxCommit(t.Context(), store.InboxRequest{Packet: raw, Mailbox: b.mailbox(), MailboxSeq: 1, GrantID: g.GrantID, Open: b.openOptions(b.state.Peers[bid]), Receipt: func(protocol.ReceiptPayload) ([]byte, error) { return wire, nil }}); err != nil {
		t.Fatal(err)
	}
	b.state, err = loadState(b.options.StateDir, b.sealer)
	if err != nil {
		t.Fatal(err)
	}
	// Atomic receipt custody already proves admission. A now-unusable incoming
	// grant must not turn post-commit journal cleanup into a permanent blocker.
	b.state.Peers[bid].Incoming = "unavailable-after-commit"
	if err = b.recoverReserved(t.Context()); err != nil {
		t.Fatal(err)
	}
	items, err := b.db.ListInbox(t.Context(), 10)
	if err != nil || len(items) != 1 {
		t.Fatal("duplicate custody admission")
	}
	next, err := b.makePacket(bid, b.state.Peers[bid], protocol.Coauthor, "next", nil)
	if err != nil || next.Message.SenderSeq != 2 {
		t.Fatal("receipt sequence reused")
	}
}

type failedSealer struct{ store.Sealer }

func (failedSealer) Seal([]byte, []byte) ([]byte, error) { return nil, fault.New(store.SealingFailed) }

func TestJournalFailureFencesFurtherEffects(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	a.sealer = failedSealer{a.sealer}
	if _, err := a.Dispatch(t.Context(), localipc.Request{Method: "send", Session: a.state.Label, Peer: peerID(b.state.Chain), Kind: "coauthor", Body: "unsaved"}); err == nil {
		t.Fatal("save failure accepted")
	}
	reserved := a.state.Reserved
	if reserved == nil {
		t.Fatal("no reserved test packet")
	}
	if err := a.Sync(context.Background()); err == nil {
		t.Fatal("failed journal recovered without restart")
	}
	exists, err := a.db.OutboxExists(t.Context(), reserved.Message.MessageID)
	if err != nil || exists {
		t.Fatal("unsynced sender sequence reached custody")
	}
}
