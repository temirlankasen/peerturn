package daemon

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestCodexCapabilityHelloReportsVerifiedBoundRuntimeVersion(t *testing.T) {
	parallelDaemonTest(t)
	_, receiver, runtime := nativePair(t)
	result, err := receiver.Dispatch(t.Context(), localipc.Request{Method: "hello", Session: receiver.state.Label, NativeThread: receiver.state.Codex.ThreadID})
	if err != nil {
		t.Fatal(err)
	}
	var hello struct {
		Capability     string `json:"capability"`
		RuntimeVersion string `json:"runtime_version"`
	}
	decodeResult(t, result, &hello)
	if hello.Capability == "" || hello.RuntimeVersion != receiver.state.Codex.Version {
		t.Fatalf("native hello omitted exact bound runtime version: %+v", hello)
	}
	runtime.failure = "stale"
	before := len(receiver.capabilities)
	if _, err = receiver.Dispatch(t.Context(), localipc.Request{Method: "hello", Session: receiver.state.Label, NativeThread: receiver.state.Codex.ThreadID}); localipc.PublicError(err).Code != "needs_rebind" || len(receiver.capabilities) != before {
		t.Fatalf("unproven runtime acquired a version-bearing capability: %v", err)
	}
}
