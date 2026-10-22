package codex

import (
	"context"
	"errors"
	"testing"
)

func TestSandboxPreflightFailsOnMissingUserNamespace(t *testing.T) {
	calls := 0
	probe := sandboxCanaries{run: func(context.Context, []string) ([]byte, error) {
		calls++
		return nil, errors.New("No permissions to create a new namespace")
	}, exists: func() bool { return false }}
	if err := probe.check(t.Context()); responderReason(err) != "sandbox_unavailable" || calls != 1 {
		t.Fatalf("calls %d error %v", calls, err)
	}
}

func TestSandboxPreflightRejectsWriteCanaryEvenWhenNativeCommandFails(t *testing.T) {
	calls := 0
	probe := sandboxCanaries{run: func(context.Context, []string) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte(sandboxReadControl), nil
		}
		return nil, errors.New("exit 1")
	}, exists: func() bool { return true }}
	if err := probe.check(t.Context()); responderReason(err) != "sandbox_unavailable" {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("network probed after failed write boundary: %d", calls)
	}
}

func TestSandboxPreflightRequiresWriteAndNetworkDenial(t *testing.T) {
	for _, networkAllowed := range []bool{false, true} {
		calls := 0
		probe := sandboxCanaries{run: func(context.Context, []string) ([]byte, error) {
			calls++
			if calls == 1 {
				return []byte(sandboxReadControl), nil
			}
			if calls == 3 && networkAllowed {
				return nil, nil
			}
			return nil, errors.New("sandbox denial")
		}, exists: func() bool { return false }}
		err := probe.check(t.Context())
		if (err == nil) == networkAllowed {
			t.Fatalf("network allowed %v error %v", networkAllowed, err)
		}
	}
}

func TestSandboxPreflightRejectsObservedNetworkEvenOnCommandError(t *testing.T) {
	calls := 0
	probe := sandboxCanaries{run: func(context.Context, []string) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte(sandboxReadControl), nil
		}
		return nil, errors.New("failure after connect")
	},
		exists: func() bool { return false }, networkObserved: func() bool { return true }}
	if err := probe.check(t.Context()); responderReason(err) != "sandbox_unavailable" {
		t.Fatal(err)
	}
}
