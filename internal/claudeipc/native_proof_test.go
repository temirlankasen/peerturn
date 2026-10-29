package claudeipc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDaemonNativeProofPinsProcessAndSocketIncarnation(t *testing.T) {
	path := filepath.Join(currentDirectory(t), "native.sock")
	listener := currentListener(t, path)
	proof := CaptureNativeProcess(t.Context(), path)
	if proof == nil || !proof.Valid(t.Context()) {
		t.Fatal("live private native socket did not establish continuity")
	}
	for _, field := range []string{"pid", "uid", "started"} {
		t.Run(field, func(t *testing.T) {
			other := *proof
			switch field {
			case "pid":
				other.peer.pid++
			case "uid":
				other.peer.uid++
			case "started":
				other.peer.started++
			}
			if other.Valid(t.Context()) {
				t.Fatal("different native process incarnation accepted")
			}
		})
	}
	if proof.HasDescendant(t.Context(), os.Getpid()) || proof.HasDescendant(t.Context(), 0) {
		t.Fatal("native self or unavailable caller PID accepted as descendant")
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	currentListener(t, path)
	if proof.Valid(t.Context()) {
		t.Fatal("replacement socket accepted under the same native process")
	}
	var missing *NativeProcessProof
	if missing.Valid(t.Context()) || missing.HasDescendant(t.Context(), os.Getpid()) {
		t.Fatal("missing platform identity proof admitted a caller")
	}
}
