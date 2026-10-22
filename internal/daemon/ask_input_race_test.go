package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/claude"
	"github.com/temirlankasen/peerturn/internal/keystore"
)

type heldAskKeyStore struct {
	keystore.Backend
	entered chan struct{}
	release chan struct{}
}

func (b *heldAskKeyStore) Load(ctx context.Context, id string) ([]byte, error) {
	close(b.entered)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.release:
		return b.Backend.Load(ctx, id)
	}
}

func TestAskInputKeyringWaitAllowsRevokeAndRechecksAuthority(t *testing.T) {
	a, b, run, _, instance := askLifecycleFixture(t)
	if err := a.observeAsk(t.Context(), run, claude.ResponderStarted, "22222222-2222-4222-8222-222222222222", instance); err != nil {
		t.Fatal(err)
	}
	backend := &heldAskKeyStore{Backend: a.authorityKeyStore, entered: make(chan struct{}), release: make(chan struct{})}
	a.authorityKeyStore = backend
	defer close(backend.release)
	done := make(chan error, 1)
	submitted := make(chan struct{}, 1)
	go func() {
		done <- a.admitAskInput(t.Context(), run, instance, "fork", func() error { submitted <- struct{}{}; return nil })
	}()
	<-backend.entered
	revoked := make(chan error, 1)
	go func() {
		a.opMu.Lock()
		defer a.opMu.Unlock()
		a.mu.Lock()
		defer a.mu.Unlock()
		g, err := a.incomingGrant(a.state.Peers[peerID(b.state.Chain)])
		if err == nil {
			err = a.db.GrantRevokeLocal(t.Context(), g.GrantID, g.GrantEpoch, unixNow())
		}
		revoked <- err
	}()
	select {
	case err := <-revoked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("keyring wait held the owner operation fence")
	}
	backend.release <- struct{}{}
	if err := <-done; err == nil {
		t.Fatal("revoked input passed the final authority check")
	}
	if len(submitted) != 0 {
		t.Fatal("revoked input reached native stdin")
	}
}
