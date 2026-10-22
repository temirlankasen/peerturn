package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

type expiringCaller struct {
	hellos, sends, effects int
	failure                string
}

type renewingLeaseCaller struct {
	expiringCaller
	opened, closed int
	reopenedEarly  bool
}

func (c *renewingLeaseCaller) OpenCapability(ctx context.Context, session string) (json.RawMessage, func() error, error) {
	if c.opened != c.closed {
		c.reopenedEarly = true
	}
	c.opened++
	result, err := c.Call(ctx, localipc.Request{Method: "hello", Session: session})
	return result, func() error { c.closed++; return nil }, err
}

func (c *expiringCaller) Call(_ context.Context, request localipc.Request) (json.RawMessage, error) {
	switch request.Method {
	case "hello":
		c.hellos++
		if c.hellos == 1 {
			return json.RawMessage(`{"capability":"expired-capability"}`), nil
		}
		return json.RawMessage(`{"capability":"fresh-capability"}`), nil
	case "send":
		c.sends++
		if request.Capability == "expired-capability" {
			return nil, localipc.NewError(c.failure, "unbound")
		}
		c.effects++
	}
	return json.RawMessage(`{"state":"stored"}`), nil
}

func TestMCPRenewsExpiredCapabilityBeforeSingleAdmittedSend(t *testing.T) {
	c := &expiringCaller{failure: "unbound"}
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(renewalInput()), &output, Options{Session: "chosen", Caller: c}); err != nil {
		t.Fatal(err)
	}
	if c.hellos != 2 || c.sends != 2 || c.effects != 1 || strings.Contains(output.String(), `"isError":true`) {
		t.Fatalf("expired authority was not renewed before admission: %+v %s", c, output.String())
	}
}

func TestMCPNeverRenewsOrResubmitsAmbiguousSend(t *testing.T) {
	c := &expiringCaller{failure: "in_doubt"}
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(renewalInput()), &output, Options{Session: "chosen", Caller: c}); err != nil {
		t.Fatal(err)
	}
	if c.hellos != 1 || c.sends != 1 || !strings.Contains(output.String(), `"isError":true`) {
		t.Fatalf("ambiguous send was automatically retried: %+v %s", c, output.String())
	}
}

func TestMCPRenewalClosesPreviousIPCLease(t *testing.T) {
	c := &renewingLeaseCaller{failure: "unbound"}
	var output bytes.Buffer
	if err := Serve(t.Context(), strings.NewReader(renewalInput()), &output, Options{Session: "chosen", Caller: c}); err != nil {
		t.Fatal(err)
	}
	if c.opened != 2 || c.closed != 2 || c.reopenedEarly {
		t.Fatalf("renewal retained an old IPC lease: %+v", c)
	}
}

func renewalInput() string {
	return `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"fixture","version":"1"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"peer_send","arguments":{"peer":"known-peer","kind":"coauthor","body":"explicit"}}}` + "\n"
}
