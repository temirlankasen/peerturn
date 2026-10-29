package daemon

import (
	"slices"
	"testing"

	"github.com/temirlankasen/peerturn/internal/claude"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
	"github.com/temirlankasen/peerturn/internal/store"
)

func TestRuntimeStatusSeparatesVersionAndCapabilities(t *testing.T) {
	for _, runtime := range []string{runtimepolicy.Claude, runtimepolicy.Codex} {
		version, binding := "2.1.308", "native_claude"
		if runtime == runtimepolicy.Codex {
			version, binding = "0.164.0", "codex_shared_daemon"
		}
		s := Status{Binding: binding, RuntimeVersion: version, Capabilities: Capabilities{AttachExisting: true, WakeIdle: true, InformBoundary: true}}
		finishRuntimeReport(&s)
		if s.RuntimePolicy == nil || s.RuntimePolicy.Runtime != runtime || s.RuntimePolicy.Status != runtimepolicy.NewerUnverified {
			t.Fatalf("missing actual runtime verification: %+v", s.RuntimePolicy)
		}
		if !slices.Contains(s.RuntimePolicy.AvailableCapabilities, "coauthor") || !slices.Contains(s.RuntimePolicy.AvailableCapabilities, "owner_invitation") {
			t.Fatal("unverified newer runtime lost full capabilities")
		}
		s.Capabilities.WakeIdle = false
		failure := runtimepolicy.ContractFailure(runtime, version, "queue", "probe", "coauthor")
		s.RuntimePolicy.Contracts = []runtimepolicy.Contract{{ID: "queue", Status: "failed", Capabilities: failure.AffectedCapabilities, Action: failure.Action}}
		finishRuntimeReport(&s)
		if slices.Contains(s.RuntimePolicy.AvailableCapabilities, "coauthor") || !slices.Contains(s.RuntimePolicy.AvailableCapabilities, "owner_invitation") || !slices.Contains(s.RuntimePolicy.AvailableCapabilities, "local_revoke") {
			t.Fatalf("contract failure affected independent capability: %+v", s.RuntimePolicy)
		}
	}
}

func TestRuntimeStatusDoesNotClaimUnobservedHook(t *testing.T) {
	s := Status{Binding: "codex_shared_daemon", RuntimeVersion: "0.163.0", Capabilities: Capabilities{AttachExisting: true}}
	finishRuntimeReport(&s)
	for _, contract := range s.RuntimePolicy.Contracts {
		if contract.ID == "owner_boundary" && contract.Status == "not_observed" {
			return
		}
	}
	t.Fatal("missing hook observation was reported as compatibility")
}

func TestRuntimeStatusReportsUnknownClaudeAfterHelperLoss(t *testing.T) {
	d, _ := pairedStores(t)
	bindClaudeVersion(t, d, "2.1.308")
	d.claudeBinding = nil
	s := claudeRuntimeStatus(t, d)
	if s.RuntimePolicy.Runtime != runtimepolicy.Claude || s.RuntimePolicy.Status != runtimepolicy.Unknown || s.Capabilities.AttachExisting {
		t.Fatalf("lost helper fabricated version/readiness: %+v", s)
	}
}

func TestAskRejectsUnverifiedBoundRuntimeBeforePreparation(t *testing.T) {
	d, _ := pairedStores(t)
	instance := bindClaudeVersion(t, d, "2.1.308")
	preflight := d.checkResponderStartup()
	if !slices.Contains(preflight.Failures, "runtime_version_unqualified") {
		t.Fatal("newer session inherited ask qualification")
	}
	_, _, err := d.authorizePreparedAsk(t.Context(), askBudgetReservation{}, store.InboxEntry{},
		d.state.NativeSessionID, instance, "", claude.ResponderOptions{}, nil)
	if localipc.PublicError(err).Code != "responder_unavailable" {
		t.Fatalf("ask preparation reached unverified runtime: %v", err)
	}
}
