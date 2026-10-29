package daemon

import (
	"slices"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

func TestClaudeNewerRuntimeCapabilitiesAndFailureIsolation(t *testing.T) {
	for _, contract := range []string{"claude.lifecycle", "claude.owner_boundary", "claude.ui.toast"} {
		t.Run(contract, func(t *testing.T) {
			d, _ := pairedStores(t)
			instance := bindClaudeVersion(t, d, "2.1.308")
			s := claudeRuntimeStatus(t, d)
			if s.RuntimePolicy.Status != runtimepolicy.NewerUnverified || !s.Capabilities.WakeIdle || !s.Capabilities.InformBoundary {
				t.Fatalf("new runtime lost existing functionality: %+v", s)
			}
			request := localipc.Request{Method: "adapter.contract", Instance: "stale", Kind: contract}
			if _, err := d.Dispatch(t.Context(), request); localipc.PublicError(err).Code != "needs_rebind" {
				t.Fatalf("stale helper changed capabilities: %v", err)
			}
			request.Instance = instance
			if _, err := d.Dispatch(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			s = claudeRuntimeStatus(t, d)
			if !s.Capabilities.AttachExisting || s.Capabilities.WakeIdle != (contract != "claude.lifecycle") || s.Capabilities.InformBoundary != (contract != "claude.owner_boundary") {
				t.Fatalf("failure crossed capability boundary: %+v", s.Capabilities)
			}
			if len(d.claudeBinding.ContractFailures) != 1 || !slices.Contains(s.RuntimePolicy.AvailableCapabilities, "local_revoke") {
				t.Fatal("failure or owner recovery missing from diagnostics")
			}
			found := false
			for _, check := range s.RuntimePolicy.Contracts {
				if check.ID == contract && check.Status == "failed" && check.Action != "" {
					found = true
				}
			}
			if !found {
				t.Fatal("contract failure has no bounded recovery diagnostic")
			}
			if contract == "claude.lifecycle" {
				value, err := d.Dispatch(t.Context(), localipc.Request{Method: "adapter.poll", Instance: instance})
				event, ok := value.(adapterEvent)
				if err != nil || !ok || event.Type != "status" {
					t.Fatalf("coauthor polling survived lifecycle loss: %v %v", value, err)
				}
				if _, err = d.Dispatch(t.Context(), localipc.Request{Method: "adapter.authorize", Instance: instance}); localipc.PublicError(err).Code != "runtime_contract_changed" {
					t.Fatalf("lost lifecycle authorized input: %v", err)
				}
			}
			request.Kind = "private-marker"
			if _, err := d.Dispatch(t.Context(), request); localipc.PublicError(err).Code != "invalid_arguments" {
				t.Fatal("arbitrary contract accepted", err)
			}
		})
	}
}

func claudeRuntimeStatus(t *testing.T, d *Daemon) Status {
	t.Helper()
	value, err := d.Dispatch(t.Context(), localipc.Request{Method: "status"})
	status, ok := value.(Status)
	if err != nil || !ok || status.RuntimePolicy == nil {
		t.Fatalf("runtime diagnostic missing: %v %v", value, err)
	}
	return status
}

func TestClaudeClockSleepClosesAllRuntimeCapabilities(t *testing.T) {
	d, _ := pairedStores(t)
	instance := bindClaude(t, d)
	_, err := d.Dispatch(t.Context(), localipc.Request{Method: "adapter.contract", Instance: instance, Kind: "claude.clock.sleep"})
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range []string{"coauthor", "owner_invitation", "inform_boundary", "inform_notification"} {
		if d.claudeBinding.CapabilityAvailable(capability) {
			t.Errorf("missing clock.sleep retained dependent capability %s", capability)
		}
	}
	if _, err = d.Dispatch(t.Context(), localipc.Request{Method: "status"}); err != nil {
		t.Fatal("runtime contract failure blocked owner recovery", err)
	}
}

func TestClaudeCurrentRequiresConfirmedLiveSameSocket(t *testing.T) {
	d, _ := pairedStores(t)
	request := localipc.Request{Method: "adapter.current", NativeSocket: "/tmp/claude.sock"}
	if _, err := d.Dispatch(t.Context(), request); localipc.PublicError(err).Code != "needs_rebind" {
		t.Fatal("missing binding resolved", err)
	}
	_, err := d.Dispatch(t.Context(), localipc.Request{Method: "adapter.open", NativeSession: "11111111-1111-4111-8111-111111111111", NativeSocket: request.NativeSocket, RuntimeVersion: "2.1.308", Provider: "gateway"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Dispatch(t.Context(), request); localipc.PublicError(err).Code != "needs_rebind" {
		t.Fatal("unconfirmed binding resolved", err)
	}
	instance := bindClaudeVersion(t, d, "2.1.308")
	_, err = d.Dispatch(t.Context(), request)
	if err == nil || localipc.PublicError(err).Code != "needs_rebind" {
		t.Fatal("confirmed binding resolved without kernel caller credentials", err)
	}
	request.NativeSocket = "/tmp/other.sock"
	if _, err = d.Dispatch(t.Context(), request); localipc.PublicError(err).Code != "needs_rebind" {
		t.Fatal("different socket resolved", err)
	}
	request.NativeSocket = "/tmp/claude.sock"
	if err = d.claudeBinding.Touch(instance, time.Now().Add(21*time.Second)); err == nil {
		t.Fatal("fixture failed to expire lease")
	}
	if _, err = d.Dispatch(t.Context(), request); localipc.PublicError(err).Code != "needs_rebind" {
		t.Fatal("expired binding resolved", err)
	}
}
