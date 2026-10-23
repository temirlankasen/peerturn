package daemon

import (
	"path/filepath"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestClaudeProjectIsRecordedOnlyAfterNativeChallenge(t *testing.T) {
	parallelDaemonTest(t)
	dir := fileAuthorityFixture(t)
	d, err := Open(t.Context(), Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := d.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := localipc.Request{Method: "adapter.open", NativeSession: "11111111-1111-4111-8111-111111111111", NativeSocket: "/tmp/claude.sock", RuntimeVersion: "2.1.306", Provider: "gateway", WorkingDirectory: project}
	value, err := d.Dispatch(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	opened, ok := value.(map[string]string)
	if !ok || d.state.ClaudeProject != "" {
		t.Fatal("project became bound before challenge")
	}
	value, err = d.Dispatch(t.Context(), localipc.Request{Method: "adapter.challenge", NativeSocket: r.NativeSocket})
	if err != nil {
		t.Fatal(err)
	}
	challenged, ok := value.(map[string]string)
	if !ok {
		t.Fatal("missing challenge")
	}
	_, err = d.Dispatch(t.Context(), localipc.Request{Method: "adapter.confirm", NativeSession: r.NativeSession, Instance: opened["instance"], Nonce: challenged["nonce"]})
	if err != nil || d.state.ClaudeProject != project {
		t.Fatal("native project not bound", err)
	}
}

func TestKeystoreCapabilityDoesNotEnableCodexOrUnboundReceiver(t *testing.T) {
	parallelDaemonTest(t)
	dir := fileAuthorityFixture(t)
	backend := &memoryKeyStore{}
	if err := MigrateAuthoritySecrets(t.Context(), dir, backend); err != nil {
		t.Fatal(err)
	}
	d, err := Open(t.Context(), Options{StateDir: dir, KeyStore: backend})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := d.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	status := Status{}
	d.mu.Lock()
	d.responderStatus(t.Context(), &status)
	d.mu.Unlock()
	if !status.Capabilities.AuthoritySecretsIsolated || status.Capabilities.ForkResponder || status.Responder.Available || status.ResponderReason != "native_binding_unavailable" {
		t.Fatal("keystore alone granted responder authority", status.Responder)
	}
	backend.unavailable = true
	d.mu.Lock()
	d.responderStatus(t.Context(), &status)
	d.mu.Unlock()
	if status.Capabilities.AuthoritySecretsIsolated || status.Capabilities.ForkResponder {
		t.Fatal("stale successful keystore open accepted")
	}
}
