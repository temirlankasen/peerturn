package daemon

import (
	"crypto/ecdh"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// expiringPacket commits an own packet that expires in two seconds.
func expiringPacket(t *testing.T, from, to *Daemon, body string) (reservedPacket, int64) {
	t.Helper()
	id := peerID(to.state.Chain)
	r, err := from.makePacket(id, from.state.Peers[id], protocol.Coauthor, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Keep a full second for custody even near a Unix-second boundary.
	expires := unixNow() + 2
	r.Message.ExpiresAt = expires
	r.Packet.Header.ExpiresAt = expires
	pub, err := ecdh.X25519().NewPublicKey(to.cert.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	if r.Packet, err = envelope.Seal(r.Packet.Header, r.Message, from.signingKey(), pub); err != nil {
		t.Fatal(err)
	}
	from.state.Reserved = &r
	from.state.Sequences[id]++
	if err = from.save(); err != nil {
		t.Fatal(err)
	}
	if err = from.recoverReserved(t.Context()); err != nil {
		t.Fatal(err)
	}
	return r, expires
}

func waitUntil(unix int64) {
	timer := time.NewTimer(time.Until(time.Unix(unix, 0)))
	defer timer.Stop()
	<-timer.C
}

// A packet that expired before the peer received it used to fence the channel
// until a new invitation, or park every later message at the peer forever when
// the relay had already stored it. The sender now announces the lost sequence,
// and the peer admits the next message.
func TestExpiredUndeliveredSequenceIsAnnouncedAsLost(t *testing.T) {
	for _, relayed := range []bool{false, true} {
		t.Run(map[bool]string{false: "never_relayed", true: "relayed_never_received"}[relayed], func(t *testing.T) {
			parallelDaemonTest(t)
			a, b := pairedStores(t)
			aid := peerID(b.state.Chain)
			a.lossGrace = 0
			lost, expires := expiringPacket(t, a, b, "lost before delivery")
			if relayed {
				if err := a.db.MarkRelayed(t.Context(), lost.Message.MessageID, unixNow()); err != nil {
					t.Fatal(err)
				}
				delete(a.state.Pending, lost.Message.MessageID)
			}
			waitUntil(expires)
			a.lastMaintenance = 0
			if err := a.maintain(t.Context()); err != nil {
				t.Fatal(err)
			}
			p := a.state.Peers[aid]
			if p.SequenceBlocked || p.Floor != 1 || p.Update == nil {
				t.Fatalf("loss not announced: blocked=%t floor=%d update=%t", p.SequenceBlocked, p.Floor, p.Update != nil)
			}
			next, err := a.makePacket(aid, p, protocol.Coauthor, "after loss", nil)
			if err != nil {
				t.Fatalf("channel still fenced: %v", err)
			}
			if err = b.receive(t.Context(), next.Packet, 1); err != nil {
				t.Fatal(err)
			}
			if got := inboxBodies(t, b); len(got) != 0 {
				t.Fatalf("message past a gap admitted before the floor: %v", got)
			}
			if err = b.receive(t.Context(), *p.Update, 2); err != nil {
				t.Fatal(err)
			}
			if got := inboxBodies(t, b); len(got) != 1 || got[0] != "after loss" {
				t.Fatalf("announced floor did not release the parked message: %v", got)
			}
		})
	}
}

func inboxBodies(t *testing.T, d *Daemon) []string {
	t.Helper()
	page, err := d.inbox(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	bodies := []string{}
	inbox, ok := page.(InboxPage)
	if !ok {
		t.Fatalf("inbox page %T", page)
	}
	for _, m := range inbox.Messages {
		bodies = append(bodies, m.BodyUTF8)
	}
	return bodies
}

// Receipts spend the same sequence but are filed under the reverse channel.
// A later receipt does not lift the floor over an earlier unrelayed packet.
func TestLossFloorCountsLiveReceipts(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	aid := peerID(b.state.Chain)
	a.lossGrace = 0
	_, expires := expiringPacket(t, a, b, "lost before delivery")
	commitData(t, a, b, "still live")
	incoming := incomingData(t, b, a)
	b.state.Reserved = &incoming
	b.state.Sequences[peerID(a.state.Chain)]++
	if err := b.recoverReserved(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := a.receive(t.Context(), incoming.Packet, 1); err != nil {
		t.Fatal(err)
	}
	if a.state.Sequences[aid] != 3 {
		t.Fatalf("receipt did not spend a sequence: %d", a.state.Sequences[aid])
	}
	waitUntil(expires)
	a.lastMaintenance = 0
	if err := a.maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if p := a.state.Peers[aid]; p.Floor != 1 {
		t.Fatalf("floor %d covers a live sequence", p.Floor)
	}
}

// A clock set back can expire a later packet before an earlier one. The floor
// never covers a packet still waiting for the relay.
func TestLossFloorStaysBelowUnrelayedPackets(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	aid := peerID(b.state.Chain)
	a.lossGrace = 0
	commitData(t, a, b, "waiting for the relay")
	lost, expires := expiringPacket(t, a, b, "expires first")
	if err := a.db.MarkRelayed(t.Context(), lost.Message.MessageID, unixNow()); err != nil {
		t.Fatal(err)
	}
	delete(a.state.Pending, lost.Message.MessageID)
	waitUntil(expires)
	a.lastMaintenance = 0
	if err := a.maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if p := a.state.Peers[aid]; p.Floor != 0 {
		t.Fatalf("floor %d covers an unrelayed packet", p.Floor)
	}
}

// A floor over a packet the peer already holds parked admits that packet and
// skips only the sequences never received.
func TestAnnouncedFloorKeepsHeldPackets(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	aid := peerID(b.state.Chain)
	commitData(t, a, b, "never arrives")
	held := commitData(t, a, b, "held parked")
	if err := b.receive(t.Context(), held.Packet, 1); err != nil {
		t.Fatal(err)
	}
	p := a.state.Peers[aid]
	p.Floor = 2
	if err := a.scheduleUpdate(p); err != nil {
		t.Fatal(err)
	}
	if err := b.receive(t.Context(), *p.Update, 2); err != nil {
		t.Fatal(err)
	}
	if got := inboxBodies(t, b); len(got) != 1 || got[0] != "held parked" {
		t.Fatalf("floor dropped a held packet: %v", got)
	}
}

// A clock set back can expire a middle packet first. Once the packets around
// it are relayed, the floor skips the lost one at once: a later packet held
// parked must not wait for an earlier packet's longer expiry and lapse.
func TestLossFloorSkipsMiddleLossWithoutWaiting(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	aid := peerID(b.state.Chain)
	a.lossGrace = 0
	first := commitData(t, a, b, "first")
	lost, expires := expiringPacket(t, a, b, "expires first")
	third := commitData(t, a, b, "parked behind the loss")
	for _, r := range []reservedPacket{first, lost, third} {
		if err := a.db.MarkRelayed(t.Context(), r.Message.MessageID, unixNow()); err != nil {
			t.Fatal(err)
		}
		delete(a.state.Pending, r.Message.MessageID)
	}
	for i, r := range []reservedPacket{first, third} {
		if err := b.receive(t.Context(), r.Packet, uint64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	waitUntil(expires)
	a.lastMaintenance = 0
	if err := a.maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	p := a.state.Peers[aid]
	if p.Floor < 2 || p.Update == nil {
		t.Fatalf("floor %d waits for a live earlier packet", p.Floor)
	}
	if err := b.receive(t.Context(), *p.Update, 3); err != nil {
		t.Fatal(err)
	}
	if got := inboxBodies(t, b); len(got) != 2 {
		t.Fatalf("parked packet not released: %v", got)
	}
}
