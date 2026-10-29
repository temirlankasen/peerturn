package claudeipc

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// ResolveCurrentEvidence reads an already confirmed binding. Its native socket
// connection inspects only kernel peer credentials: no auth or user frame is sent.
func ResolveCurrentEvidence(ctx context.Context, client localipc.Client) (CurrentEvidence, error) {
	socket := os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET")
	before, err := nativeSocketInfo(socket)
	if err != nil {
		return CurrentEvidence{}, currentResolutionError()
	}
	conn, peer, err := connectNative(ctx, socket)
	if err != nil {
		return CurrentEvidence{}, currentResolutionError()
	}
	defer closeNative(conn)
	if !nativeAncestor(peer) {
		return CurrentEvidence{}, currentResolutionError()
	}
	raw, err := client.Call(ctx, localipc.Request{Method: "adapter.current", NativeSocket: socket})
	if err != nil {
		return CurrentEvidence{}, currentResolutionError()
	}
	var evidence CurrentEvidence
	if json.Unmarshal(raw, &evidence) != nil || evidence.Session == "" || len(evidence.Session) > 160 {
		return CurrentEvidence{}, currentResolutionError()
	}
	epoch, err := hex.DecodeString(evidence.BindingEpoch)
	if err != nil || len(epoch) != 16 || hex.EncodeToString(epoch) != evidence.BindingEpoch {
		return CurrentEvidence{}, currentResolutionError()
	}
	after, statErr := nativeSocketInfo(socket)
	current, peerErr := connectedNativeProcess(conn)
	if statErr != nil || !os.SameFile(before, after) || peerErr != nil || current != peer || !nativeAncestor(peer) {
		return CurrentEvidence{}, currentResolutionError()
	}
	return evidence, nil
}

func nativeAncestor(peer nativeProcess) bool {
	return ancestorChain(os.Getpid(), peer, processWithParent)
}

// Every edge and process incarnation is rechecked to reject PID reuse/reparenting.
func ancestorChain(child int, peer nativeProcess, inspect func(int) (nativeProcess, int, error)) bool {
	type entry struct {
		process nativeProcess
		parent  int
	}
	chain := make([]entry, 0, 16)
	seen := map[int]bool{}
	for depth := range 64 {
		process, parent, err := inspect(child)
		if err != nil || process.pid != child || process.uid != peer.uid || process.started == 0 || seen[child] {
			return false
		}
		seen[child] = true
		chain = append(chain, entry{process, parent})
		if len(chain) > 1 && process.started > chain[len(chain)-2].process.started {
			return false
		}
		if depth > 0 && process == peer {
			for _, expected := range chain {
				current, currentParent, currentErr := inspect(expected.process.pid)
				if currentErr != nil || current != expected.process || currentParent != expected.parent {
					return false
				}
			}
			return true
		}
		if parent <= 0 {
			return false
		}
		child = parent
	}
	return false
}

func currentResolutionError() error {
	problem := localipc.NewError("needs_rebind", "needs_rebind")
	problem.Message = "Run peerturn session bind --current from the selected session's tool child, then rerun status/doctor or reconnect MCP."
	return problem
}
