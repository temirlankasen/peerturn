package daemon

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/mcp"
	"github.com/temirlankasen/peerturn/internal/relayclient"
)

type claudeJoinCaller struct {
	daemon  *Daemon
	trigger string
	lose    func() error
}

func (c *claudeJoinCaller) Call(ctx context.Context, request localipc.Request) (json.RawMessage, error) {
	if request.Method == c.trigger && c.lose != nil {
		lose := c.lose
		c.lose = nil
		if err := lose(); err != nil {
			return nil, err
		}
	}
	result, err := c.daemon.Dispatch(ctx, request)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func TestClaudeMCPJoinRejectsLostOrReplacedNativeBinding(t *testing.T) {
	for _, mode := range []string{"close", "expired", "replaced", "unbind", "unbind_status", "unbind_rebind"} {
		t.Run(mode, func(t *testing.T) {
			admin := routingKey(t)
			relay := newRelayFixture(t, admin)
			issuer := openRecoveryDaemon(t, relay, admin)
			joiner := openRecoveryDaemon(t, relay, routingKey(t))
			instance := bindClaude(t, joiner)
			created, err := issuer.createInvite(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var invitation struct {
				URL string `json:"url"`
			}
			decodeResult(t, created, &invitation)
			ownerPastedInvite(t, joiner, invitation.URL)
			epoch := joiner.claudeBinding.MCPEpoch()
			caller := &claudeJoinCaller{daemon: joiner, trigger: "join.text"}
			caller.lose = func() error {
				switch mode {
				case "expired":
					if touchErr := joiner.claudeBinding.Touch(instance, time.Now().Add(21*time.Second)); touchErr == nil {
						t.Fatal("fixture did not expire the same helper incarnation")
					}
					return nil
				case "unbind", "unbind_status", "unbind_rebind":
					_, err = joiner.Dispatch(t.Context(), localipc.Request{Method: "session.unbind"})
				default:
					_, err = joiner.Dispatch(t.Context(), localipc.Request{Method: "adapter.close", Instance: instance})
				}
				if err == nil && (mode == "replaced" || mode == "unbind_rebind") {
					bindClaude(t, joiner)
				}
				return err
			}
			input := claudeJoinInput(t, invitation.URL)
			if mode == "unbind_status" {
				caller.trigger = "status"
				input = strings.Replace(input, `{"jsonrpc":"2.0","id":2,"method":"tools/call"`, `{"jsonrpc":"2.0","id":3,"method":"tools/call"`, 1)
				first, rest, _ := strings.Cut(input, "\n")
				input = first + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"peer_status","arguments":{}}}` + "\n" + rest
			}
			var output bytes.Buffer
			if err = mcp.Serve(t.Context(), strings.NewReader(input), &output, mcp.Options{Session: joiner.state.Label, Caller: caller, ExpectedBindingEpoch: epoch}); err != nil {
				t.Fatal(err)
			}
			grants, readErr := joiner.db.ListGrants(t.Context())
			if readErr != nil || len(joiner.state.Joining) != 0 || len(grants) != 0 || !strings.Contains(output.String(), "needs_rebind") {
				t.Fatalf("lost %s provenance admitted onboarding: joins=%d grants=%d error=%v", mode, len(joiner.state.Joining), len(grants), readErr)
			}
			if _, err = joiner.Dispatch(t.Context(), localipc.Request{Method: "status"}); err != nil {
				t.Fatalf("lost peer provenance restricted an owner inspection: %v", err)
			}
			if mode != "replaced" && mode != "unbind_rebind" {
				bindClaude(t, joiner)
			}
			var freshOutput bytes.Buffer
			ownerPastedInvite(t, joiner, invitation.URL)
			fresh := &claudeJoinCaller{daemon: joiner}
			if err = mcp.Serve(t.Context(), strings.NewReader(claudeJoinInput(t, invitation.URL)), &freshOutput, mcp.Options{Session: joiner.state.Label, Caller: fresh, ExpectedBindingEpoch: joiner.claudeBinding.MCPEpoch()}); err != nil {
				t.Fatal(err)
			}
			if len(joiner.state.Joining) != 1 || !strings.Contains(freshOutput.String(), "waiting_inviter") {
				t.Fatal("a freshly verified native incarnation could not join")
			}
		})
	}
}

func TestClaudeExpiredBindingCannotIssueNewMCPCapability(t *testing.T) {
	d, _ := pairedStores(t)
	instance := bindClaude(t, d)
	if err := d.claudeBinding.Touch(instance, time.Now().Add(21*time.Second)); err == nil {
		t.Fatal("fixture did not expire helper provenance")
	}
	if _, err := d.Dispatch(t.Context(), localipc.Request{Method: "hello", Session: d.state.Label}); localipc.PublicError(err).Code != "needs_rebind" {
		t.Fatalf("lost Claude provenance minted new MCP authority: %v", err)
	}
	if _, err := d.Dispatch(t.Context(), localipc.Request{Method: "status"}); err != nil {
		t.Fatalf("owner inspection was blocked by expired peer provenance: %v", err)
	}
}

func TestClaudeMCPJoinRechecksLeaseAfterInviteFetch(t *testing.T) {
	admin := routingKey(t)
	relay := newRelayFixture(t, admin)
	issuer := openRecoveryDaemon(t, relay, admin)
	joiner := openRecoveryDaemon(t, relay, routingKey(t))
	instance := bindClaude(t, joiner)
	created, err := issuer.createInvite(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var invitation struct {
		URL string `json:"url"`
	}
	decodeResult(t, created, &invitation)
	ownerPastedInvite(t, joiner, invitation.URL)
	transport, ok := relay.server.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatal("fixture requires the standard verified TLS transport")
	}
	transport = transport.Clone()
	var expired atomic.Bool
	transport.TLSClientConfig.VerifyConnection = func(tls.ConnectionState) error {
		joiner.mu.Lock()
		defer joiner.mu.Unlock()
		if touchErr := joiner.claudeBinding.Touch(instance, time.Now().Add(21*time.Second)); touchErr == nil {
			return errors.New("fixture did not expire helper provenance during fetch")
		}
		expired.Store(true)
		return nil
	}
	joiner.relay, err = relayclient.New(relayclient.Options{URL: relay.server.URL, Key: routePrivate(joiner.state.RoutingSeed), HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = mcp.Serve(t.Context(), strings.NewReader(claudeJoinInput(t, invitation.URL)), &output, mcp.Options{Session: joiner.state.Label, Caller: &claudeJoinCaller{daemon: joiner}, ExpectedBindingEpoch: joiner.claudeBinding.MCPEpoch()}); err != nil {
		t.Fatal(err)
	}
	grants, err := joiner.db.ListGrants(t.Context())
	if err != nil || !expired.Load() || len(joiner.state.Joining) != 0 || len(grants) != 0 || !strings.Contains(output.String(), "needs_rebind") {
		t.Fatalf("lease expired during fetch still issued authority: joins=%d grants=%d error=%v", len(joiner.state.Joining), len(grants), err)
	}
}

func claudeJoinInput(t *testing.T, link string) string {
	t.Helper()
	arguments, err := json.Marshal(map[string]string{"text": link})
	if err != nil {
		t.Fatal(err)
	}
	return `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"fixture","version":"1"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"peer_join","arguments":` + string(arguments) + "}}\n"
}

func TestJoinCapabilityRechecksCapabilityLifetime(t *testing.T) {
	for _, loss := range []string{"missing", "idle_expired", "absolute_expired", "session_changed"} {
		t.Run(loss, func(t *testing.T) {
			d, _ := pairedStores(t)
			capability := provenJoinCapability(t, d)
			if err := d.checkJoinCapability(capability); err != nil {
				t.Fatal(err)
			}
			binding := d.capabilities[capability]
			switch loss {
			case "missing":
				delete(d.capabilities, capability)
			case "idle_expired":
				binding.expiresAt = time.Now().Add(-time.Second)
				d.capabilities[capability] = binding
			case "absolute_expired":
				binding.absoluteExpiry = time.Now().Add(-time.Second)
				d.capabilities[capability] = binding
			case "session_changed":
				binding.session = "other"
				d.capabilities[capability] = binding
			}
			if err := d.checkJoinCapability(capability); err == nil {
				t.Fatal("lost capability remained eligible for join effects")
			}
		})
	}
}
