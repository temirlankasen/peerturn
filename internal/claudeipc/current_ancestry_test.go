package claudeipc

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestCurrentAncestorRequiresStableExactChain(t *testing.T) {
	for _, mode := range []string{"valid", "self", "sibling", "wrong_uid", "reused_pid", "reparented", "loop"} {
		t.Run(mode, func(t *testing.T) {
			peer := nativeProcess{pid: 10, uid: 1000, started: 10}
			processes := map[int]nativeProcess{30: {pid: 30, uid: 1000, started: 30}, 20: {pid: 20, uid: 1000, started: 20}, 10: peer}
			parents := map[int]int{30: 20, 20: 10, 10: 1}
			if mode == "self" {
				peer = processes[30]
			}
			if mode == "sibling" {
				peer.pid = 11
			}
			if mode == "wrong_uid" {
				process := processes[20]
				process.uid++
				processes[20] = process
			}
			if mode == "loop" {
				parents[20] = 30
			}
			calls := map[int]int{}
			inspect := func(pid int) (nativeProcess, int, error) {
				calls[pid]++
				process := processes[pid]
				parent := parents[pid]
				if calls[pid] > 1 && pid == 20 {
					if mode == "reused_pid" {
						process.started++
					}
					if mode == "reparented" {
						parent++
					}
				}
				return process, parent, nil
			}
			if ancestorChain(30, peer, inspect) != (mode == "valid") {
				t.Fatal("ancestry proof accepted a different process incarnation or parent chain")
			}
		})
	}
}

func TestResolveCurrentRejectsNonAncestorWithoutNativeBytes(t *testing.T) {
	native := currentListener(t, filepath.Join(currentDirectory(t), "native.sock"))
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", native.Addr().String())
	client := currentDaemon(t, func(context.Context, localipc.Request) (any, error) {
		t.Error("a non-ancestor reached daemon binding lookup")
		return nil, rebind()
	})
	done := make(chan error, 1)
	go func() {
		connection, err := native.AcceptUnix()
		if err != nil {
			done <- err
			return
		}
		defer closeNative(connection)
		data := make([]byte, 1)
		count, readErr := connection.Read(data)
		if count != 0 || !errors.Is(readErr, io.EOF) {
			done <- io.ErrUnexpectedEOF
			return
		}
		done <- nil
	}()
	_, err := ResolveCurrentEvidence(t.Context(), client)
	assertCurrentRebind(t, err)
	if !strings.Contains(localipc.PublicError(err).Message, "session bind --current") {
		t.Fatal("read-only resolution failure has no explicit bind recovery action")
	}
	if err = <-done; err != nil {
		t.Fatal("native probe wrote data", err)
	}
	process, parent, err := processWithParent(os.Getpid())
	if err != nil || process.pid != os.Getpid() || parent != os.Getppid() {
		t.Fatal("platform ancestry source does not identify this process", err)
	}
}
