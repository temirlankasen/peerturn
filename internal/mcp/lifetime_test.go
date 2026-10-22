package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

type releaseCaller struct {
	recordingCaller
	releaseContextErr error
}

func (c *releaseCaller) Call(ctx context.Context, request localipc.Request) (json.RawMessage, error) {
	if request.Method == "bye" {
		c.releaseContextErr = ctx.Err()
	}
	return c.recordingCaller.Call(ctx, request)
}

func TestMCPEOFReleasesBoundCapability(t *testing.T) {
	c := &releaseCaller{}
	if err := Serve(t.Context(), strings.NewReader(""), io.Discard, Options{Session: "chosen", Caller: c}); err != nil {
		t.Fatal(err)
	}
	if len(c.requests) != 2 || c.requests[1].Method != "bye" || c.requests[1].Capability != "bound-token" || c.requests[1].Session != "chosen" {
		t.Fatalf("stdio EOF leaked daemon capability: %#v", c.requests)
	}
}

func TestMCPCancellationUsesLiveReleaseContext(t *testing.T) {
	c := &releaseCaller{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Serve(ctx, strings.NewReader(""), io.Discard, Options{Session: "chosen", Caller: c}); err == nil {
		t.Fatal("canceled MCP succeeded")
	}
	if len(c.requests) != 2 || c.requests[1].Method != "bye" || c.releaseContextErr != nil {
		t.Fatalf("cancellation did not release capability with a usable context: %#v %v", c.requests, c.releaseContextErr)
	}
}
