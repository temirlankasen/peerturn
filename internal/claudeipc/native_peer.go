package claudeipc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type nativeProcess struct {
	pid     int
	uid     uint32
	started uint64
}

// The helper is spawned directly by the pinned Claude process. Bash clients use
// the nonce round trip instead because their immediate parent can be a shell.
func newNativeGuard(ctx context.Context) (func(context.Context) error, error) {
	parent, err := processIdentity(os.Getppid())
	if err != nil {
		return nil, rebind()
	}
	return newNativeGuardFor(ctx, os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET"), parent)
}

func newNativeGuardFor(ctx context.Context, socket string, parent nativeProcess) (func(context.Context) error, error) {
	info, err := nativeSocketInfo(socket)
	if err != nil {
		return nil, rebind()
	}
	guard := func(ctx context.Context) error {
		current, statErr := nativeSocketInfo(socket)
		if statErr != nil || !os.SameFile(info, current) {
			return rebind()
		}
		conn, peer, connectErr := connectNative(ctx, socket)
		if connectErr != nil {
			return rebind()
		}
		if closeErr := conn.Close(); closeErr != nil || peer != parent {
			return rebind()
		}
		return nil
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	return guard, nil
}

func connectNative(ctx context.Context, socket string) (*net.UnixConn, nativeProcess, error) {
	before, err := nativeSocketInfo(socket)
	if err != nil {
		return nil, nativeProcess{}, rebind()
	}
	dialer := net.Dialer{Timeout: 2 * time.Second}
	raw, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, nativeProcess{}, rebind()
	}
	conn, ok := raw.(*net.UnixConn)
	if !ok {
		closeNative(raw)
		return nil, nativeProcess{}, rebind()
	}
	peer, err := connectedNativeProcess(conn)
	after, statErr := nativeSocketInfo(socket)
	if err != nil || statErr != nil || !os.SameFile(before, after) {
		closeNative(conn)
		return nil, nativeProcess{}, rebind()
	}
	return conn, peer, nil
}

func connectedNativeProcess(conn *net.UnixConn) (nativeProcess, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return nativeProcess{}, rebind()
	}
	var pid int
	var uid uint32
	var credentialErr error
	if err = raw.Control(func(fd uintptr) {
		pid, uid, credentialErr = nativePeerCredentials(int(fd))
	}); err != nil || credentialErr != nil || pid <= 0 || int64(uid) != int64(os.Geteuid()) {
		return nativeProcess{}, rebind()
	}
	peer, err := processIdentity(pid)
	if err != nil || peer.uid != uid || peer.started == 0 {
		return nativeProcess{}, rebind()
	}
	return peer, nil
}

func nativeSocketInfo(socket string) (os.FileInfo, error) {
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket {
		return nil, rebind()
	}
	root, err := os.OpenRoot(filepath.Dir(socket))
	if err != nil {
		return nil, rebind()
	}
	info, err := root.Lstat(filepath.Base(socket))
	if closeErr := root.Close(); err != nil || closeErr != nil || !privateNativeInfo(info, int64(os.Geteuid())) {
		return nil, rebind()
	}
	return info, nil
}

func privateNativeInfo(info os.FileInfo, uid int64) bool {
	if info == nil || info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return false
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(owner.Uid) == uid
}

func closeNative(conn net.Conn) {
	if err := conn.Close(); err != nil {
		return
	}
}
