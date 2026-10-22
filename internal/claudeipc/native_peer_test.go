package claudeipc

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestNativePeerKernelCredentials(t *testing.T) {
	listener := currentListener(t, filepath.Join(currentDirectory(t), "native.sock"))
	conn, peer, err := connectNative(t.Context(), listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	want, err := processIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if peer != want || peer.pid != os.Getpid() || int64(peer.uid) != int64(os.Geteuid()) {
		t.Fatal("native kernel credentials do not identify the socket owner")
	}
}

func TestNativeGuardChecksExactProcessIncarnation(t *testing.T) {
	listener := currentListener(t, filepath.Join(currentDirectory(t), "native.sock"))
	parent, err := processIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	guard, err := newNativeGuardFor(t.Context(), listener.Addr().String(), parent)
	if err != nil {
		t.Fatal(err)
	}
	if err = guard(t.Context()); err != nil {
		t.Fatal("live native parent failed revalidation")
	}
	for _, field := range []string{"pid", "uid", "start"} {
		t.Run(field, func(t *testing.T) {
			other := parent
			switch field {
			case "pid":
				other.pid++
			case "uid":
				other.uid++
			case "start":
				other.started++
			}
			_, err := newNativeGuardFor(t.Context(), listener.Addr().String(), other)
			assertCurrentRebind(t, err)
		})
	}
}

func TestNativeGuardRejectsInheritedSocketFromAnotherParent(t *testing.T) {
	listener := currentListener(t, filepath.Join(currentDirectory(t), "native.sock"))
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", listener.Addr().String())
	_, err := newNativeGuard(t.Context())
	assertCurrentRebind(t, err)
}

func TestNativeGuardRejectsReplacedSocket(t *testing.T) {
	path := filepath.Join(currentDirectory(t), "native.sock")
	listener := currentListener(t, path)
	parent, err := processIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	guard, err := newNativeGuardFor(t.Context(), path, parent)
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	currentListener(t, path)
	assertCurrentRebind(t, guard(t.Context()))
}

func TestNativeSocketRejectsOtherUID(t *testing.T) {
	listener := currentListener(t, filepath.Join(currentDirectory(t), "native.sock"))
	info, err := os.Lstat(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("native socket lacks Unix ownership")
	}
	if !privateNativeInfo(info, int64(owner.Uid)) || privateNativeInfo(info, int64(owner.Uid)+1) {
		t.Fatal("socket ownership did not enforce the required UID")
	}
}
