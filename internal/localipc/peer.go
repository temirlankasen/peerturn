package localipc

import (
	"context"
	"net"
)

type peerPIDKey struct{}

// PeerPID is supplied only by the accepted Unix connection's kernel credentials.
// Zero means unavailable; process-bound operations must refuse it.
func PeerPID(ctx context.Context) int {
	pid, ok := ctx.Value(peerPIDKey{}).(int)
	if !ok {
		return 0
	}
	return pid
}

func withPeerPID(ctx context.Context, conn *net.UnixConn) context.Context {
	raw, err := conn.SyscallConn()
	if err != nil {
		return ctx
	}
	var pid int
	var credentialErr error
	if err = raw.Control(func(fd uintptr) { pid, credentialErr = socketPID(int(fd)) }); err != nil || credentialErr != nil || pid <= 0 {
		return ctx
	}
	return context.WithValue(ctx, peerPIDKey{}, pid)
}
