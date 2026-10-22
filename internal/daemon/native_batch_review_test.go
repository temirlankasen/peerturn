package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestReviewFailedBatchCleanupCannotSpendSequenceTwice(t *testing.T) {
	_, b, _ := nativePair(t)
	peer := b.peers()[0].ID
	start := b.state.Sequences[peer]
	batch := &reservedPacket{}
	for range 2 {
		prepared, err := b.makePacket(peer, b.state.Peers[peer], protocol.Coauthor, "uncommitted reservation", nil)
		if err != nil {
			t.Fatal(err)
		}
		batch.NativeBatch = append(batch.NativeBatch, prepared)
		b.state.Sequences[peer]++
	}
	b.state.Reserved = batch
	if err := b.save(); err != nil {
		t.Fatal(err)
	}
	if _, exists := b.state.Pending[""]; exists {
		t.Fatal("batch journal created an empty outbound retry")
	}
	injected := errors.New("second lookup failed")
	calls := 0
	err := b.recoverNativeBatch(t.Context(), batch, func(context.Context, string) (bool, error) {
		calls++
		if calls == 2 {
			return false, injected
		}
		return false, nil
	})
	if !errors.Is(err, injected) || b.state.Sequences[peer] != start+2 || b.state.Reserved != batch {
		t.Fatal("partial cleanup changed recoverable sequence state")
	}
	if err = b.recoverReserved(t.Context()); err != nil {
		t.Fatal(err)
	}
	if b.state.Reserved != nil || b.state.Sequences[peer] != start {
		t.Fatal("batch retry spent sequence twice")
	}
	for _, packet := range batch.NativeBatch {
		exists, lookupErr := b.db.OutboxExists(t.Context(), packet.Packet.Header.MessageID)
		if lookupErr != nil || exists {
			t.Fatal("uncommitted batch published packets")
		}
	}
}
