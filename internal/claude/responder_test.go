package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const responderParent = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"

func responderFixture(t *testing.T) *Responder {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	options := ResponderOptions{
		Executable: executable, WorkingDirectory: t.TempDir(), HomeDirectory: t.TempDir(),
		RuntimeEnv: []string{"ANTHROPIC_BASE_URL=https://gateway.example.invalid", "ANTHROPIC_AUTH_TOKEN=fixture-runtime-value"},
		AuthorityPaths: ResponderAuthorityPaths{StateDirectory: t.TempDir(),
			ConfigFile: filepath.Join(t.TempDir(), "config.toml"), RuntimeDirectory: t.TempDir()},
		AuthorityCheck: func(context.Context) error { return nil },
		PrepareFork:    func(context.Context, string) error { return nil },
		Timeout:        3 * time.Second,
	}
	configuration, err := responderConfiguration(executable, options.WorkingDirectory, options.HomeDirectory, "", options.RuntimeEnv)
	if err != nil {
		t.Fatal(err)
	}
	options, err = ConfigureResponder(options, &configuration)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewResponder(options)
	if err != nil {
		t.Fatal(err)
	}
	r.preflight = func(context.Context, ResponderOptions) error { return nil }
	r.options.AdmitPeerInput = func(_ context.Context, _ string, submit func() error) error { return submit() }
	return r
}

func requireResponderFailure(t *testing.T, result AskResult, err error, code ResponderFailure) {
	t.Helper()
	var failure *ResponderError
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("failure = %v; want %s", err, code)
	}
	if result.Answer != "" {
		t.Fatal("failure returned an answer")
	}
}

func TestResponderAuthorityGatePreventsSpawn(t *testing.T) {
	r := responderFixture(t)
	for _, check := range []func(context.Context) error{nil, func(context.Context) error {
		return errors.New("private path and credential must not escape")
	}} {
		r.options.AuthorityCheck = check
		result, err := r.Run(t.Context(), AskRequest{Snapshot: responderSnapshotFixture(t, r), NativeSessionID: responderParent, FramedQuestion: "success"})
		requireResponderFailure(t, result, err, ProtectedPathPolicyFailed)
		if len(result.Stages) != 0 || strings.Contains(err.Error(), "credential") {
			t.Fatal("authority gate leaked details or claimed process start")
		}
	}
}

func TestResponderRejectsUntrustedLaunchFields(t *testing.T) {
	r := responderFixture(t)
	for _, req := range []AskRequest{
		{NativeSessionID: "--permission-mode=bypassPermissions", FramedQuestion: "success"},
		{NativeSessionID: responderParent, FramedQuestion: ""},
		{NativeSessionID: responderParent, FramedQuestion: string([]byte{0xff})},
		{NativeSessionID: responderParent, FramedQuestion: strings.Repeat("a", 65537)},
	} {
		result, err := r.Run(t.Context(), req)
		requireResponderFailure(t, result, err, InvalidResponderRequest)
	}
}

func TestResponderFinalAnswerAndStages(t *testing.T) {
	r := responderFixture(t)
	result, err := r.Run(t.Context(), AskRequest{Snapshot: responderSnapshotFixture(t, r), NativeSessionID: responderParent, FramedQuestion: "success"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "Useful final answer" || result.Redactions != 0 {
		t.Fatalf("unexpected final answer: %+v", result)
	}
	if len(result.Stages) != 4 || result.Stages[0] != ResponderStarted || result.Stages[1] != ResponderModelInput ||
		result.Stages[2] != ResponderCompleted || result.Stages[3] != OutboundRedacted {
		t.Fatalf("stages = %v", result.Stages)
	}
}

func TestResponderRejectsPartialMalformedAndNonzeroOutput(t *testing.T) {
	r := responderFixture(t)
	for _, mode := range []string{"partial", "malformed", "wrong-type", "error-result", "same-session", "nonzero", "empty", "duplicate", "invalid-utf8"} {
		t.Run(mode, func(t *testing.T) {
			result, err := r.Run(t.Context(), AskRequest{Snapshot: responderSnapshotFixture(t, r), NativeSessionID: responderParent, FramedQuestion: mode})
			requireResponderFailure(t, result, err, ResponderFailed)
			if len(result.Stages) < 1 || result.Stages[0] != ResponderStarted {
				t.Fatalf("unsupported completion evidence: %v", result.Stages)
			}
		})
	}
}

func TestResponderBoundsOutputAndDeadline(t *testing.T) {
	r := responderFixture(t)
	r.options.MaxOutputBytes = 512
	result, err := r.Run(t.Context(), AskRequest{Snapshot: responderSnapshotFixture(t, r), NativeSessionID: responderParent, FramedQuestion: "oversized"})
	requireResponderFailure(t, result, err, ResponderBudgetExceeded)
	r.options.Timeout = 50 * time.Millisecond
	result, err = r.Run(t.Context(), AskRequest{Snapshot: responderSnapshotFixture(t, r), NativeSessionID: responderParent, FramedQuestion: "sleep"})
	requireResponderFailure(t, result, err, ResponderTimeout)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err = r.Run(ctx, AskRequest{Snapshot: responderSnapshotFixture(t, r), NativeSessionID: responderParent, FramedQuestion: "success"})
	requireResponderFailure(t, result, err, ResponderCancelled)
}

func TestResponderBoundsRedactedAnswerExpansion(t *testing.T) {
	r := responderFixture(t)
	r.options.MaxOutputBytes = 512
	r.options.KnownSecrets = []string{"x"}
	result, err := r.Run(t.Context(), AskRequest{Snapshot: responderSnapshotFixture(t, r), NativeSessionID: responderParent, FramedQuestion: "redaction-expansion"})
	requireResponderFailure(t, result, err, ResponderBudgetExceeded)
}

func TestResponderConcurrencyUsesExactParent(t *testing.T) {
	r := responderFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	r.options.AuthorityCheck = func(context.Context) error {
		close(started)
		<-release
		return nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.Run(t.Context(), AskRequest{Snapshot: responderSnapshotFixture(t, r), NativeSessionID: responderParent, FramedQuestion: "success"})
		done <- err
	}()
	<-started
	result, err := r.Run(t.Context(), AskRequest{Snapshot: responderSnapshotFixture(t, r), NativeSessionID: responderParent, FramedQuestion: "success"})
	requireResponderFailure(t, result, err, ResponderBusy)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestResponderRequiresAbsoluteTrustedConfiguration(t *testing.T) {
	r := responderFixture(t)
	for _, options := range []ResponderOptions{
		{Executable: "claude", WorkingDirectory: t.TempDir(), HomeDirectory: t.TempDir()},
		{Executable: "/bin/claude", WorkingDirectory: "relative", HomeDirectory: t.TempDir()},
		{Executable: "/bin/claude", WorkingDirectory: t.TempDir(), HomeDirectory: "relative"},
		{Executable: "/bin/claude", WorkingDirectory: t.TempDir(), HomeDirectory: t.TempDir(), ProtectedPaths: []string{"relative"}},
		{Executable: "/bin/claude", WorkingDirectory: t.TempDir(), HomeDirectory: t.TempDir(), RuntimeEnv: []string{"PEERTURN_TOKEN=hidden"}},
	} {
		options.AuthorityPaths = r.options.AuthorityPaths
		if _, err := NewResponder(options); err == nil {
			t.Fatal("accepted unsafe configuration")
		}
	}
	r.options.Executable = filepath.Join(t.TempDir(), "missing")
	result, err := r.Run(t.Context(), AskRequest{Snapshot: responderSnapshotFixture(t, r), NativeSessionID: responderParent, FramedQuestion: "success"})
	requireResponderFailure(t, result, err, ResponderUnavailable)
}
