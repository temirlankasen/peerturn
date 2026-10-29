package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

func TestClaudeAdapterStartupFailureIsTyped(t *testing.T) {
	for _, tc := range []struct{ version, want string }{
		{"2.1.305", "runtime_unsupported"},
		{"unknown-private-marker", "runtime_version_unknown"},
		{"2.1.306", "needs_rebind"},
		{"2.1.308", "needs_rebind"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
			t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "")
			t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "synthetic-private-marker")
			var stdout, stderr bytes.Buffer
			code := runClaudeAdapter([]string{"claude", "--native-session", "session-a", "--runtime-version", tc.version, "--provider", "gateway"}, strings.NewReader(""), &stdout, &stderr)
			if code == 0 || stdout.Len() != 0 || stderr.String() != "peerturn: "+tc.want+"\n" {
				t.Fatalf("helper failure lost its safe reason: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestAdapterStartupDiagnosticsNeverExposeErrorDetails(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&os.PathError{Op: "open", Path: "/synthetic-private-marker", Err: os.ErrPermission}, "permission_denied"},
		{&localipc.Error{Code: "uid_mismatch", Message: "synthetic-private-marker"}, "peer_identity_failed"},
		{&localipc.Error{Code: "unavailable", Message: "synthetic-private-marker"}, "daemon_unavailable"},
		{&localipc.Error{Code: "needs_rebind\nsynthetic-private-marker", Message: "synthetic-private-marker"}, "adapter_unavailable"},
		{errors.New("synthetic-private-marker"), "adapter_unavailable"},
		{runtimepolicy.ContractFailure(runtimepolicy.Claude, "2.1.308", "claude.lifecycle", "runtime", "coauthor"), "runtime_contract_changed"},
	} {
		var stderr bytes.Buffer
		if code := writeClaudeFailure(&stderr, tc.err); code == 0 || stderr.String() != "peerturn: "+tc.want+"\n" {
			t.Fatalf("diagnostic escaped the fixed vocabulary: code=%d stderr=%q", code, stderr.String())
		}
	}
}
