package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func pendingJoinRequest(t *testing.T, d *Daemon, capability, handle string) localipc.Request {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"method": "join.owner", "scope": "coauthor", "session": d.state.Label, "capability": capability, "invitation_handle": handle})
	if err != nil {
		t.Fatal(err)
	}
	var r localipc.Request
	if err = json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOwnerPendingClaudeJoinWithoutModelSecret(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	relay := newRelayFixture(t, admin)
	issuer := openRecoveryDaemon(t, relay, admin)
	d := openRecoveryDaemon(t, relay, routingKey(t))
	created, err := issuer.createInvite(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var invitation struct{ URL string }
	decodeResult(t, created, &invitation)
	capability := provenJoinCapability(t, d)
	ownerPastedInvite(t, d, invitation.URL)
	result, err := d.Dispatch(t.Context(), pendingJoinRequest(t, d, capability, ""))
	if err != nil || len(d.state.Joining) != 1 || len(d.joinOrigins.owner) != 0 {
		t.Fatalf("hook-carried invitation did not join once: %v", err)
	}
	raw, err := json.Marshal(result)
	if err != nil || bytes.Contains(raw, []byte("#v=1")) {
		t.Fatal("join exposed invitation capability")
	}
	_, err = d.Dispatch(t.Context(), pendingJoinRequest(t, d, capability, ""))
	if localipc.PublicError(err).Code != "no_owner_invitation" {
		t.Fatal("used invitation remained pending")
	}
}

func TestOwnerPendingHandlesExpiryAndWipes(t *testing.T) {
	parallelDaemonTest(t)
	d, _ := pairedStores(t)
	capability := provenJoinCapability(t, d)
	first := syntheticJoinLink("one.invalid", "A")
	second := syntheticJoinLink("two.invalid", "B")
	ownerPastedInvite(t, d, first+"\n"+second)
	result, err := d.Dispatch(t.Context(), pendingJoinRequest(t, d, capability, ""))
	if localipc.PublicError(err).Code != "ambiguous_owner_invitation" {
		t.Fatalf("multiple pending invitations were not ambiguous: %v", err)
	}
	status, statusErr := d.Dispatch(t.Context(), localipc.Request{Method: "status", Session: d.state.Label, Capability: capability})
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	errorOutput, encodeErr := json.Marshal(localipc.PublicError(err))
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	outputs := fmt.Sprint(result, string(errorOutput), status)
	raw, encodeErr := json.Marshal(status)
	if encodeErr != nil || !bytes.Contains(raw, []byte("owner_invitations")) {
		t.Fatal("status omitted pending handles")
	}
	var view struct {
		Invitations []struct{ Handle string } `json:"owner_invitations"`
	}
	if json.Unmarshal(raw, &view) != nil || len(view.Invitations) != 2 || len(view.Invitations[0].Handle) == 0 {
		t.Fatal("status lacks two selectable handles")
	}
	_, err = d.Dispatch(t.Context(), pendingJoinRequest(t, d, capability, view.Invitations[0].Handle))
	if localipc.PublicError(err).Code != "audience_mismatch" || len(d.joinOrigins.owner) != 1 {
		t.Fatalf("handle did not select and consume exactly one invitation: %v", err)
	}
	for hash, record := range d.joinOrigins.owner {
		record.expires = time.Now().Add(-time.Second)
		d.joinOrigins.owner[hash] = record
	}
	_, err = d.Dispatch(t.Context(), pendingJoinRequest(t, d, capability, ""))
	if localipc.PublicError(err).Code != "owner_invitation_expired" || len(d.joinOrigins.owner) != 0 {
		t.Fatal("expired pending capability was not wiped")
	}
	outputs += fmt.Sprint(localipc.PublicError(err))
	ownerPastedInvite(t, d, first)
	if _, err = d.Dispatch(t.Context(), localipc.Request{Method: "adapter.close", Instance: d.claudeBinding.Instance}); err != nil {
		t.Fatal(err)
	}
	if len(d.joinOrigins.owner) != 0 {
		t.Fatal("rebind retained pending capability")
	}
	bindClaude(t, d)
	capability = provenJoinCapability(t, d)
	_, err = d.Dispatch(t.Context(), pendingJoinRequest(t, d, capability, ""))
	if localipc.PublicError(err).Code != "no_owner_invitation" {
		t.Fatal("rebound session inherited pending invitation")
	}
	ownerPastedInvite(t, d, first)
	stateDir := d.options.StateDir
	for _, secret := range []string{first, second, "#v=1", "&ik=", "&ek=", strings.Repeat("a", 64)} {
		if strings.Contains(outputs+string(raw), secret) {
			t.Fatal("captured status/error output contains a capability fragment")
		}
	}
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			data, readErr := root.ReadFile(entry.Name())
			if readErr != nil || bytes.Contains(data, []byte(first)) {
				t.Fatal("pending owner capability reached disk")
			}
		}
	}
	if err = d.Close(); err != nil || len(d.joinOrigins.owner) != 0 {
		t.Fatal("daemon stop retained pending capability")
	}
	reopened, err := Open(t.Context(), d.options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if len(reopened.joinOrigins.owner) != 0 {
		t.Fatal("restart restored a pending capability")
	}
}
