package claudeipc

import (
	"context"
	"os"
)

// NativeProcessProof pins the native socket's kernel UID/PID/start and inode.
// It proves process continuity, not human origin or the native executable's code.
type NativeProcessProof struct {
	socket string
	info   os.FileInfo
	peer   nativeProcess
}

// CaptureNativeProcess sends no native bytes and returns no proof on unsupported
// platforms or when private socket ownership/process identity is unavailable.
func CaptureNativeProcess(ctx context.Context, socket string) *NativeProcessProof {
	info, err := nativeSocketInfo(socket)
	if err != nil {
		return nil
	}
	conn, peer, err := connectNative(ctx, socket)
	if err != nil {
		return nil
	}
	closeNative(conn)
	current, err := nativeSocketInfo(socket)
	if err != nil || !os.SameFile(info, current) {
		return nil
	}
	return &NativeProcessProof{socket: socket, info: info, peer: peer}
}

// Valid rechecks the recorded native incarnation without replacing it.
func (p *NativeProcessProof) Valid(ctx context.Context) bool {
	if p == nil {
		return false
	}
	current := CaptureNativeProcess(ctx, p.socket)
	return current != nil && current.peer == p.peer && os.SameFile(p.info, current.info)
}

// HasDescendant requires every same-UID ancestry edge and start time to remain
// stable, between checks of the original native process/socket incarnation.
func (p *NativeProcessProof) HasDescendant(ctx context.Context, pid int) bool {
	return pid > 0 && p.Valid(ctx) && ancestorChain(pid, p.peer, processWithParent) && p.Valid(ctx)
}
