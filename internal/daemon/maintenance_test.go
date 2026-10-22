package daemon

import (
	"crypto/ecdh"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestExpiredUnrelayedSequenceRequiresFreshBinding(t *testing.T) {
	a, b := pairedStores(t)
	aid := peerID(b.state.Chain)
	bid := peerID(a.state.Chain)
	r, err := a.makePacket(aid, a.state.Peers[aid], protocol.Coauthor, "expires locally", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Keep a full second for custody even near a Unix-second boundary.
	expires := unixNow() + 2
	r.Message.ExpiresAt = expires
	r.Packet.Header.ExpiresAt = expires
	pub, err := ecdh.X25519().NewPublicKey(b.cert.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	r.Packet, err = envelope.Seal(r.Packet.Header, r.Message, a.signingKey(), pub)
	if err != nil {
		t.Fatal(err)
	}
	a.state.Reserved = &r
	a.state.Sequences[aid]++
	if err = a.save(); err != nil {
		t.Fatal(err)
	}
	if err = a.recoverReserved(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = b.receive(t.Context(), r.Packet, 1); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(time.Until(time.Unix(expires, 0)))
	defer timer.Stop()
	<-timer.C
	if err = a.maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err = a.makePacket(aid, a.state.Peers[aid], protocol.Coauthor, "cannot skip", nil)
	if localipc.ExitCode(err) != 3 || !a.state.Peers[aid].SequenceBlocked {
		t.Fatalf("expired gap silently skipped: %v", err)
	}
	// Incoming controls remain admissible when only the outgoing channel needs rebinding.
	body, err := codec.Canonical(protocol.ReceiptPayload{MessageID: r.Message.MessageID, AttemptID: r.Message.MessageID, Stage: "receiver_stored", EvidenceKind: "local_commit", At: unixNow()})
	if err != nil {
		t.Fatal(err)
	}
	control, err := b.makePacket(bid, b.state.Peers[bid], protocol.Receipt, string(body), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.receive(t.Context(), control.Packet, 1); err != nil {
		t.Fatal(err)
	}
	// Periodic retention runs through the real M1b minimization boundary.
	b.lastMaintenance = 0
	if err = b.maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	item, err := b.db.Inbox(t.Context(), r.Message.MessageID)
	if err != nil || len(item.Body) != 0 {
		t.Fatal("expired plaintext retained")
	}
}
