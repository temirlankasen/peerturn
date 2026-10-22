package localipc

import (
	"context"
	"encoding/json"
	"sync"
)

type connectionLifetimeKey struct{}
type requestSlotKey struct{}

// ConnectionLifetime is available only to a hello on a lifetime-held connection.
// Its cancellation is independent of the bounded handshake request context.
func ConnectionLifetime(ctx context.Context) (context.Context, bool) {
	lifetime, ok := ctx.Value(connectionLifetimeKey{}).(context.Context)
	return lifetime, ok
}

// OpenCapability holds one authenticated connection until its proxy closes.
// EOF, process exit and daemon shutdown all revoke the server-side capability.
func (c Client) OpenCapability(ctx context.Context, session string) (json.RawMessage, func() error, error) {
	result, conn, err := c.exchange(ctx, Request{Method: "hello", Session: session, Lease: true})
	if err != nil {
		return nil, nil, err
	}
	var once sync.Once
	var closeErr error
	closeConnection := func() error {
		once.Do(func() {
			closeErr = conn.Close()
		})
		return closeErr
	}
	stop := context.AfterFunc(ctx, func() {
		if err := closeConnection(); err != nil {
			return
		}
	})
	return result, func() error { stop(); return closeConnection() }, nil
}
