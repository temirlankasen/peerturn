package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/adapters/codex"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

type nativeFixture struct {
	thread        string
	frames, ids   []string
	failure       string
	deleted       []string
	deleteResults map[string]bool
}

func (f *nativeFixture) Bind(_ context.Context, id string) (codex.Binding, error) {
	b := codex.Binding{ThreadID: id, DaemonID: "test-daemon", Version: codex.SupportedVersion}
	return b, f.Validate(context.Background(), b)
}
func (f *nativeFixture) Validate(_ context.Context, b codex.Binding) error {
	if b.ThreadID != f.thread || b.DaemonID != "test-daemon" || b.Version != codex.SupportedVersion || f.failure == "stale" {
		return &codex.Error{Code: "needs_rebind"}
	}
	return nil
}
func (f *nativeFixture) Queue(_ context.Context, b codex.Binding, id, frame string) (codex.QueueResult, error) {
	if err := f.Validate(context.Background(), b); err != nil {
		return codex.QueueResult{}, &codex.Error{Code: "needs_rebind"}
	}
	f.frames, f.ids = append(f.frames, frame), append(f.ids, id)
	if f.failure == "in_doubt" {
		return codex.QueueResult{}, &codex.Error{Code: "in_doubt"}
	}
	return codex.QueueResult{ItemIDs: []string{"queue-" + id}, ClientUserMessageID: "peerturn:" + id}, nil
}
func (f *nativeFixture) Probe(ctx context.Context, id string) (codex.Binding, codex.Capabilities, error) {
	b, err := f.Bind(ctx, id)
	return b, codex.Capabilities{WakeIdle: err == nil}, err
}

func nativePair(t *testing.T) (*Daemon, *Daemon, *nativeFixture) {
	t.Helper()
	key := routingKey(t)
	relay := newRelayFixture(t, key)
	open := func(thread string, admin bool) (*Daemon, *nativeFixture) {
		runtime := &nativeFixture{thread: thread}
		o := InitOptions{StateDir: stateDir(t), RelayURL: relay.server.URL, SessionLabel: thread, FilePermissions: true, CodexThreadID: thread, Native: runtime}
		if admin {
			o.RoutingKey = key
		}
		if _, err := Initialize(t.Context(), o); err != nil {
			t.Fatal(err)
		}
		d, err := Open(t.Context(), Options{StateDir: o.StateDir, HTTPClient: relay.server.Client(), Native: runtime})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if closeErr := d.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		})
		return d, runtime
	}
	a, _ := open("019edaa1-1111-7111-8111-111111111111", true)
	b, runtime := open("019edaa1-2222-7222-8222-222222222222", false)
	invitation, err := a.createInvite(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	fields, ok := invitation.(map[string]any)
	if !ok {
		t.Fatal("invite result type")
	}
	url, ok := fields["url"].(string)
	if !ok {
		t.Fatal("invite URL type")
	}
	if _, err = b.join(t.Context(), url); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if err = a.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err = b.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	return a, b, runtime
}

func nativeCall(t *testing.T, d *Daemon, r localipc.Request) any {
	t.Helper()
	r.Session, r.NativeThread = d.state.Label, d.state.Codex.ThreadID
	result, err := d.Dispatch(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func nativeReceipt(t *testing.T, d *Daemon, r localipc.Request) protocol.ReceiptPayload {
	t.Helper()
	result, ok := nativeCall(t, d, r).(protocol.ReceiptPayload)
	if !ok {
		t.Fatal("receipt result type")
	}
	return result
}

func TestNativeDeliveryReceiptsInformAndRevoke(t *testing.T) {
	a, b, runtime := nativePair(t)
	peer := a.peers()[0].ID
	var ids []string
	for _, body := range []string{"first literal @path", "second https://example.invalid"} {
		r := nativeReceipt(t, a, localipc.Request{Method: "send", Peer: peer, Kind: "coauthor", Body: body})
		ids = append(ids, r.MessageID)
	}
	if err := b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(runtime.ids) != 2 || runtime.ids[0] != ids[0] || runtime.ids[1] != ids[1] {
		t.Fatalf("FIFO: %v", runtime.ids)
	}
	for _, id := range ids {
		r, err := b.db.Receipt(t.Context(), id)
		if err != nil || r.Stage != "adapter_accepted" || r.NativeInputID != "queue-"+id {
			t.Fatalf("native receipt: %+v %v", r, err)
		}
	}
	if err := b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(runtime.ids) != 2 {
		t.Fatal("duplicate native submit")
	}
	if err := a.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, err := a.db.Receipt(t.Context(), ids[0])
	if err != nil || r.Stage != "adapter_accepted" {
		t.Fatalf("signed peer receipt: %+v %v", r, err)
	}
	fyi := nativeReceipt(t, a, localipc.Request{Method: "send", Peer: peer, Kind: "inform", Body: "quiet FYI"})
	if err = b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(runtime.ids) != 2 {
		t.Fatal("inform woke native thread")
	}
	offer := nativeCall(t, b, localipc.Request{Method: "codex_inform", MessageID: "owner-turn"})
	raw, err := json.Marshal(offer)
	if err != nil || strings.Contains(string(raw), "quiet FYI") || !strings.Contains(string(raw), "peer_inbox") {
		t.Fatalf("offer: %s %v", raw, err)
	}
	offered, ok := offer.(map[string]string)
	if !ok {
		t.Fatal("hook response type")
	}
	nativeCall(t, b, localipc.Request{Method: "codex_inform_ack", MessageID: "owner-turn", Cursor: offered["offer_token"]})
	r, err = b.db.Receipt(t.Context(), fyi.MessageID)
	if err != nil || r.EvidenceKind != "hook_notice" || r.Stage != "offered" {
		t.Fatalf("hook receipt: %+v %v", r, err)
	}
	again, ok := nativeCall(t, b, localipc.Request{Method: "codex_inform", MessageID: "next-turn"}).(map[string]string)
	if !ok {
		t.Fatal("hook response type")
	}
	if again["additional_context"] != "" {
		t.Fatal("offered twice")
	}
	queued := nativeReceipt(t, a, localipc.Request{Method: "send", Peer: peer, Kind: "coauthor", Body: "revoked before delivery"})
	for _, p := range b.state.Peers {
		g, grantErr := b.incomingGrant(p)
		if grantErr != nil {
			t.Fatal(grantErr)
		}
		nativeCall(t, b, localipc.Request{Method: "revoke", GrantID: g.GrantID})
	}
	if err = b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(runtime.ids) != 2 {
		t.Fatalf("revoked %s delivered", queued.MessageID)
	}
}

func TestNativeAmbiguityNeverResubmitsAndStaleCannotRebind(t *testing.T) {
	a, b, runtime := nativePair(t)
	runtime.failure = "in_doubt"
	r := nativeReceipt(t, a, localipc.Request{Method: "send", Peer: a.peers()[0].ID, Kind: "coauthor", Body: "ambiguous"})
	if err := b.Sync(t.Context()); err == nil {
		t.Fatal("ambiguous queue accepted")
	}
	if err := b.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(runtime.ids) != 1 {
		t.Fatal("automatic retry")
	}
	receipt, err := b.db.Receipt(t.Context(), r.MessageID)
	if err != nil || receipt.Stage != "in_doubt" {
		t.Fatalf("ambiguity: %+v %v", receipt, err)
	}
	runtime.failure = "stale"
	status, ok := nativeCall(t, b, localipc.Request{Method: "status"}).(Status)
	if !ok {
		t.Fatal("status result type")
	}
	if status.State != "needs_rebind" || status.Capabilities.WakeIdle {
		t.Fatalf("stale capability: %+v", status)
	}
	for _, p := range b.state.Peers {
		g, grantErr := b.incomingGrant(p)
		if grantErr != nil {
			t.Fatal(grantErr)
		}
		nativeCall(t, b, localipc.Request{Method: "revoke", GrantID: g.GrantID})
	}
}

func TestUnsupportedNativeInitializationCreatesNoKeys(t *testing.T) {
	dir := stateDir(t)
	_, err := Initialize(t.Context(), InitOptions{StateDir: dir, RelayURL: "https://relay.example", SessionLabel: "thread", FilePermissions: true, CodexThreadID: "foreign", Native: &nativeFixture{thread: "exact"}})
	if err == nil {
		t.Fatal("unsupported initialized")
	}
	if _, err = os.Lstat(filepath.Join(dir, "seal.key")); !os.IsNotExist(err) {
		t.Fatalf("created key on failed preflight: %v", err)
	}
}
