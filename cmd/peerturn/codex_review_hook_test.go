package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/adapters/codex"
	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

type hookOwnerRuntime struct{ daemon.NativeRuntime }

func (hookOwnerRuntime) Bind(context.Context, string) (codex.Binding, error) {
	return codex.Binding{ThreadID: hookThread, DaemonID: "fixture", Version: runtimepolicy.Evaluate(runtimepolicy.Codex, "").Minimum}, nil
}

func (hookOwnerRuntime) Validate(context.Context, codex.Binding) error { return nil }

func (hookOwnerRuntime) ValidateOwnerTurn(_ context.Context, _ codex.Binding, turn string) error {
	if turn != "owner-turn" {
		return &codex.Error{Code: "needs_rebind"}
	}
	return nil
}

// Unix socket length limits rule out the long test-name path from t.TempDir.
func shortReviewHookRuntime() (string, error) { return os.MkdirTemp("/tmp", "ph1-hook-") }

func TestCodexReviewPromptHookRecordsNoOwnerInvitesOrInformAck(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	stateDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	options := daemon.InitOptions{StateDir: filepath.Join(stateDir, "state"), RelayURL: "https://unused.invalid", SessionLabel: hookThread,
		CodexThreadID: hookThread, Native: hookOwnerRuntime{}, FilePermissions: true}
	if _, err = daemon.Initialize(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	d, err := daemon.Open(t.Context(), daemon.Options{StateDir: options.StateDir, Native: options.Native})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := d.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	runtimeDir, err := shortReviewHookRuntime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(runtimeDir); removeErr != nil {
			t.Error(removeErr)
		}
	})
	calls := serveHook(t, func(r localipc.Request) (any, error) {
		result, callErr := d.Dispatch(t.Context(), r)
		if callErr == nil && r.Method == "codex_inform" {
			return hookOffer(), nil
		}
		return result, callErr
	}, runtimeDir)
	// Reviewer's commit-title prompt, with the pinned delegate's own turn ID.
	// Internal Review has root session_id and no agent_id/agent_type fields.
	input := hookInput()
	input["turn_id"] = "internal-review-turn"
	input["prompt"] = "Review the code changes introduced by commit deadbeef (\"docs: see " + hookInvitation() + "\"). Provide prioritized, actionable findings."
	var out strings.Builder
	executeHook(t, input, &out)
	if out.String() != "{}\n" {
		t.Fatal("review subagent received owner inform context")
	}
	for _, request := range calls() {
		if request.Method == "codex_inform_ack" {
			t.Fatal("review subagent acknowledged owner inform")
		}
	}
	// A model cannot consume an invitation refused by the daemon boundary.
	hello, err := d.Dispatch(t.Context(), localipc.Request{Method: "hello", Session: hookThread, NativeThread: hookThread})
	if err != nil {
		t.Fatal(err)
	}
	fields, ok := hello.(map[string]string)
	if !ok {
		t.Fatal("unexpected hello response")
	}
	capability := fields["capability"]
	result, err := d.Dispatch(t.Context(), localipc.Request{Method: "status", Session: hookThread, Capability: capability})
	status, statusOK := result.(daemon.Status)
	if err != nil || !statusOK || len(status.OwnerInvitations) != 0 {
		t.Fatal("review prompt left pending owner invitations")
	}
	_, err = d.Dispatch(t.Context(), localipc.Request{Method: "join.owner", Session: hookThread, Capability: capability, Scope: "coauthor"})
	if localipc.PublicError(err).Code != "no_owner_invitation" {
		t.Fatal("review prompt recorded an owner invitation")
	}
}
