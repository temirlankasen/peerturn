package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

type epochCaller struct {
	epochs                             []string
	hellos, attempts, admitted, closed int
}

func (c *epochCaller) Call(_ context.Context, request localipc.Request) (json.RawMessage, error) {
	switch request.Method {
	case "hello":
		index := min(c.hellos, len(c.epochs)-1)
		c.hellos++
		return json.Marshal(map[string]string{"capability": "fixture-capability", "binding_epoch": c.epochs[index]})
	case "bye":
		c.closed++
	case "join.text":
		c.attempts++
		if c.attempts == 1 {
			return nil, localipc.NewError("unbound", "unbound")
		}
		c.admitted++
	}
	return json.RawMessage(`{"state":"stored"}`), nil
}

func TestMCPRenewalCannotSwitchNativeIncarnationOrPromotePreview(t *testing.T) {
	for _, epochs := range [][]string{{"old-instance", ""}, {"old-instance", "new-instance"}, {"", "new-instance"}} {
		caller := &epochCaller{epochs: epochs}
		binding, err := bindCapability(t.Context(), Options{Session: "same-label", Caller: caller})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = binding.Call(t.Context(), localipc.Request{Method: "join.text"}); localipc.PublicError(err).Code != "needs_rebind" {
			t.Fatalf("renewal silently switched native authority: %v", err)
		}
		if caller.admitted != 0 || caller.closed != 2 {
			t.Fatal("mismatched renewal leaked authority or an IPC lease")
		}
		if err = binding.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMCPNativeRenewalKeepsExactBindingEpoch(t *testing.T) {
	caller := &epochCaller{epochs: []string{"same-instance"}}
	binding, err := bindCapability(t.Context(), Options{Session: "same-label", Caller: caller})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = binding.Call(t.Context(), localipc.Request{Method: "join.text"}); err != nil {
		t.Fatalf("healthy matching epoch could not renew: %v", err)
	}
	if caller.hellos != 2 || caller.closed != 1 || caller.admitted != 1 {
		t.Fatal("matching native epoch did not retain exactly one fresh lease")
	}
	if err = binding.Close(); err != nil {
		t.Fatal(err)
	}
}
