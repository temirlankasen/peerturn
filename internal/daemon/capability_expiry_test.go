package daemon

import (
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestCapabilityActivityCannotExtendAbsoluteLifetime(t *testing.T) {
	d, _ := pairedStores(t)
	result, err := d.Dispatch(t.Context(), localipc.Request{Method: "hello", Session: d.state.Label})
	if err != nil {
		t.Fatal(err)
	}
	var hello struct {
		Capability string `json:"capability"`
	}
	decodeResult(t, result, &hello)
	binding := d.capabilities[hello.Capability]
	if binding.absoluteExpiry.Sub(binding.expiresAt) != capabilityMaxLifetime-capabilityTTL {
		t.Fatal("new capability lacks the specified idle and absolute lifetime")
	}
	binding.expiresAt = time.Now().Add(time.Minute)
	binding.absoluteExpiry = time.Now().Add(2 * time.Minute)
	d.capabilities[hello.Capability] = binding
	request := localipc.Request{Method: "status", Session: d.state.Label, Capability: hello.Capability}
	if _, err := d.Dispatch(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	refreshed := d.capabilities[hello.Capability]
	if !refreshed.expiresAt.Equal(binding.absoluteExpiry) {
		t.Fatal("activity did not refresh the idle window within the absolute limit")
	}
	refreshed.absoluteExpiry = time.Now().Add(-time.Second)
	d.capabilities[hello.Capability] = refreshed
	if _, err := d.Dispatch(t.Context(), request); localipc.PublicError(err).Code != "unbound" {
		t.Fatalf("activity bypassed the absolute capability lifetime: %v", err)
	}
}
