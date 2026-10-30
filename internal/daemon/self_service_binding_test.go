package daemon

import (
	"reflect"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func assertSelfServiceNoBindingEffects(t *testing.T, f *selfServiceHTTPPeer, d *Daemon, before clientState) {
	t.Helper()
	trace := f.snapshot()
	if trace.discovery != 0 || trace.enroll != 0 || trace.publish != 0 || trace.poll != 0 {
		t.Fatal("binding refusal happened after relay network effects")
	}
	if !reflect.DeepEqual(before, selfServiceJournal(t, d, false)) || len(d.state.Issued) != 0 || len(d.state.Joining) != 0 || len(d.state.Peers) != 0 {
		t.Fatal("binding refusal changed local invitation custody or peer authority")
	}
}

func TestSelfServiceDaemonExactBindingRefusesBeforeDiscovery(t *testing.T) {
	parallelDaemonTest(t)
	for _, tc := range []struct {
		name, code string
	}{
		{"foreign-session", "unbound"},
		{"foreign-profile", "conflict"},
		{"missing-native-thread", "unbound"},
		{"foreign-native-thread", "binding_mismatch"},
		{"stale-native-binding", "needs_rebind"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSelfServiceHTTPPeer(t, `"self_service"`)
			native := &nativeFixture{thread: "019edaa1-1111-7111-8111-111111111111"}
			d := openSelfServiceDaemon(t, f, native)
			before := selfServiceJournal(t, d, false)
			r := localipc.Request{Method: "invite", Session: d.state.Label, Scope: "coauthor", NativeThread: native.thread}
			switch tc.name {
			case "foreign-session":
				r.Session = "another-explicit-session"
			case "foreign-profile":
				r.ProfileID = "another-profile"
			case "missing-native-thread":
				r.NativeThread = ""
			case "foreign-native-thread":
				r.NativeThread = "019edaa1-2222-7222-8222-222222222222"
			case "stale-native-binding":
				native.failure = "stale"
			}
			if _, err := d.Dispatch(t.Context(), r); safeCode(err) != tc.code {
				t.Fatalf("exact binding refusal changed: got %v, want %s", err, tc.code)
			}
			assertSelfServiceNoBindingEffects(t, f, d, before)
		})
	}
}

func TestSelfServiceDaemonReplacedClaudeEpochRefusesBeforeDiscovery(t *testing.T) {
	parallelDaemonTest(t)
	f := newSelfServiceHTTPPeer(t, `"self_service"`)
	d := openSelfServiceDaemon(t, f, nil)
	bindClaude(t, d)
	oldEpoch := d.claudeBinding.MCPEpoch()
	if _, err := d.Dispatch(t.Context(), localipc.Request{Method: "session.unbind"}); err != nil {
		t.Fatal(err)
	}
	bindClaude(t, d)
	before := selfServiceJournal(t, d, false)
	r := localipc.Request{Method: "invite", Session: d.state.Label, Scope: "coauthor", BindingEpoch: oldEpoch}
	if _, err := d.Dispatch(t.Context(), r); safeCode(err) != "needs_rebind" {
		t.Fatal("stale Claude epoch reached the self-service path", err)
	}
	assertSelfServiceNoBindingEffects(t, f, d, before)
}

func TestSelfServiceDaemonHintDoesNotGiveMCPPublicationAuthority(t *testing.T) {
	parallelDaemonTest(t)
	f := newSelfServiceHTTPPeer(t, `"self_service"`)
	native := &nativeFixture{thread: "019edaa1-1111-7111-8111-111111111111"}
	d := openSelfServiceDaemon(t, f, native)
	value, err := d.Dispatch(t.Context(), localipc.Request{Method: "hello", Session: d.state.Label, NativeThread: native.thread})
	if err != nil {
		t.Fatal(err)
	}
	hello, ok := value.(map[string]string)
	if !ok || hello["capability"] == "" {
		t.Fatal("fixture did not receive its normal MCP capability")
	}
	before := selfServiceJournal(t, d, false)
	r := localipc.Request{Method: "invite", Session: d.state.Label, NativeThread: native.thread, Scope: "coauthor", Capability: hello["capability"]}
	if _, err = d.Dispatch(t.Context(), r); safeCode(err) != "scope_denied" {
		t.Fatal("discovery self-service expanded MCP authority", err)
	}
	assertSelfServiceNoBindingEffects(t, f, d, before)
}
