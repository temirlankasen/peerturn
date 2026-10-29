package daemon

import (
	"context"
	"slices"
	"testing"

	"github.com/temirlankasen/peerturn/internal/adapters/codex"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

type nativeContractRuntime struct{ *nativeFixture }

func (r nativeContractRuntime) Probe(ctx context.Context, thread string) (codex.Binding, codex.Capabilities, error) {
	binding, err := r.Bind(ctx, thread)
	if err != nil {
		return binding, codex.Capabilities{}, err
	}
	failure := runtimepolicy.ContractFailure(runtimepolicy.Codex, binding.Version, "codex.queue_list", "bind", "coauthor")
	return binding, codex.Capabilities{AttachExisting: true, Version: binding.Version,
		Contracts: []runtimepolicy.Contract{{ID: failure.ContractID, Phase: failure.Phase, Status: "failed", Capabilities: failure.AffectedCapabilities, Action: failure.Action}}}, failure
}

func TestNativeQueueContractFailurePreservesOwnerAndLocalRecovery(t *testing.T) {
	parallelDaemonTest(t)
	_, receiver, runtime := nativePair(t)
	receiver.native = nativeContractRuntime{runtime}
	receiver.hookVerified = unixNow()
	status := nativeValue[Status](t, receiver, localipc.Request{Method: "status"})
	if status.RuntimeVersion != receiver.state.Codex.Version || !status.Capabilities.AttachExisting || status.Capabilities.WakeIdle || !status.Capabilities.InformBoundary || status.AdapterReason != "runtime_contract_changed" {
		t.Fatalf("queue failure crossed capability boundary: %+v", status)
	}
	for _, capability := range []string{"binding", "local_revoke", "owner_invitation", "inform_boundary"} {
		if !slices.Contains(status.RuntimePolicy.AvailableCapabilities, capability) {
			t.Fatalf("queue failure closed %s", capability)
		}
	}
	if slices.Contains(status.RuntimePolicy.AvailableCapabilities, "coauthor") || status.RuntimePolicy.Contracts[0].Action == "" {
		t.Fatal("failed coauthor contract has no recovery evidence")
	}
	if _, err := receiver.Dispatch(t.Context(), localipc.Request{Method: "codex_inform", Session: receiver.state.Label,
		NativeThread: receiver.state.Codex.ThreadID, MessageID: "owner-turn"}); err != nil {
		t.Fatalf("queue contract closed independently proven owner turn: %v", err)
	}
}

func TestNativeQueueContractFailurePrecedesDeliveryIntent(t *testing.T) {
	parallelDaemonTest(t)
	sender, receiver, runtime := nativePair(t)
	receiver.native = nativeContractRuntime{runtime}
	message := nativeReceipt(t, sender, localipc.Request{Method: "send", Peer: sender.peers()[0].ID, Kind: "coauthor", Body: "synthetic contract fixture"})
	if err := receiver.Sync(t.Context()); localipc.PublicError(err).Code != "runtime_contract_changed" {
		t.Fatalf("missing typed queue failure: %v", err)
	}
	receipt, err := receiver.db.Receipt(t.Context(), message.MessageID)
	if err != nil || receipt.Stage != "receiver_stored" || len(runtime.frames) != 0 {
		t.Fatalf("read-only failure became attempted delivery: %+v %v", receipt, err)
	}
	receiver.native = runtime
	if err = receiver.Sync(t.Context()); err != nil || len(runtime.frames) != 1 {
		t.Fatalf("pending custody did not recover after contract passed: %v", err)
	}
}
