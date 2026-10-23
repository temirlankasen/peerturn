package daemon

import (
	"context"
	"errors"
	"testing"
)

func syncFixturePair(ctx context.Context, connected func() bool, syncs ...func(context.Context) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if connected() {
			return nil
		}
		for _, sync := range syncs {
			if err := sync(ctx); err != nil {
				return err
			}
		}
	}
}

func TestFixturePairWaitsForBothConnections(t *testing.T) {
	inviter, joiner := 0, 0
	err := syncFixturePair(t.Context(), func() bool { return inviter >= 5 && joiner >= 7 },
		func(context.Context) error { inviter++; return nil },
		func(context.Context) error { joiner++; return nil })
	if err != nil || inviter != 7 || joiner != 7 {
		t.Fatalf("delayed handshake did not reach both connected states: %d/%d: %v", inviter, joiner, err)
	}
}

func TestFixturePairStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := syncFixturePair(ctx, func() bool { return false }, func(context.Context) error {
		t.Error("sync ran after fixture cancellation")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("fixture lost cancellation:", err)
	}
}

func TestFixturePairPreservesSyncFailure(t *testing.T) {
	failure := errors.New("fixture custody failed")
	err := syncFixturePair(t.Context(), func() bool { return false },
		func(context.Context) error { return failure },
		func(context.Context) error { t.Error("sync continued after custody failure"); return nil })
	if !errors.Is(err, failure) {
		t.Fatal("fixture masked custody failure:", err)
	}
}
