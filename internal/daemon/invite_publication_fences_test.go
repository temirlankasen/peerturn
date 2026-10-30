package daemon

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
)

// Retain a genuine claim GET response while real signed relay mutations happen.
// These are synthetic sessions, not native binding or model-delivery evidence.
func TestInvitePublicationCachedACKCannotAuthorizeNewClaim(t *testing.T) {
	parallelDaemonTest(t)
	for _, change := range []string{"deleted_mailbox", "blocked_inviter", "revoked_invite", "local_expiry", "expiry_after_revalidation", "valid_claim"} {
		t.Run(change, func(t *testing.T) {
			operator := routingKey(t)
			f := newSelfServiceE2ERelay(t, publicKey(operator[32:]), true)
			var a *Daemon
			var entry *issuedInvite
			var path string
			next := f.server.Config.Handler
			var armed, handled atomic.Bool
			mutated := make(chan error, 1)
			postGate := change == "expiry_after_revalidation" || change == "valid_claim"
			// Install before any requests, then arm after normal claim admission.
			// Forward all response bytes from the actual relay unchanged.
			f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				method := "GET"
				if postGate {
					method = "POST"
				}
				if !armed.Load() || r.Method != method || r.URL.Path != path || !handled.CompareAndSwap(false, true) {
					next.ServeHTTP(w, r)
					return
				}
				response := httptest.NewRecorder()
				next.ServeHTTP(response, r)
				if response.Code != 200 && response.Code != 201 {
					mutated <- errors.New("real relay did not produce the expected claim/publication response")
				} else {
					mutated <- publicationClaimMutation(t.Context(), f, a, entry, operator, change)
				}
				for name, values := range response.Header() {
					w.Header()[name] = append([]string(nil), values...)
				}
				w.WriteHeader(response.Code)
				if _, err := w.Write(response.Body.Bytes()); err != nil {
					t.Error("forward genuine relay response failed")
				}
			})
			a = openSelfServiceE2EDaemon(t, f, "synthetic-publication-author")
			b := openSelfServiceE2EDaemon(t, f, "synthetic-publication-recipient")
			if err := requestSelfServiceInvite(t, a); err != nil {
				t.Fatal("initial publication failed:", safeCode(err))
			}
			entry = publicationEntry(t, a)
			if !entry.publication.confirmed {
				t.Fatal("fixture has no acknowledged publication to exercise")
			}
			if _, err := b.join(t.Context(), entry.URL); err != nil {
				t.Fatal("prepare genuine recipient claim failed:", safeCode(err))
			}
			if err := b.syncJoins(t.Context()); err != nil {
				t.Fatal("normal claim/voucher admission failed:", safeCode(err))
			}
			path = "/v1/invites/" + entry.Descriptor.InviteID
			armed.Store(true)
			syncErr := a.syncWithBudget(t.Context(), 30*time.Second)
			select {
			case err := <-mutated:
				if err != nil {
					t.Fatal("controlled first-claim mutation failed:", safeCode(err))
				}
			default:
				t.Fatal("new claim bypassed its required real GET/POST revalidation gate")
			}
			if change == "valid_claim" {
				grants, err := a.db.ListGrants(t.Context())
				if syncErr != nil || err != nil || len(grants) != 2 || entry.Claim == nil || !entry.Sent || len(a.state.Peers) != 1 {
					t.Fatal("validated new claim did not preserve the normal grant/hello flow")
				}
				if entry.publication != (invitePublication{}) {
					t.Fatal("completed claim retained idle publication confirmation")
				}
				return
			}
			assertSelfServiceOwnMailboxNoPeerAuthority(t, a)
			if entry.Claim != nil || entry.ForwardGrant != "" || entry.Response != nil || entry.Failure == "" || entry.publication.confirmed {
				t.Fatal("cached ACK admitted a new claim after its first-effect fence failed")
			}
		})
	}
}

func publicationClaimMutation(ctx context.Context, f *selfServiceE2ERelay, a *Daemon, entry *issuedInvite, operator ed25519.PrivateKey, change string) error {
	a.mu.Lock()
	clean := len(a.state.Peers) == 0 && entry.Claim == nil && entry.ForwardGrant == "" && entry.Response == nil
	a.mu.Unlock()
	grants, err := a.db.ListGrants(ctx)
	if err != nil || len(grants) != 0 || !clean {
		return errors.New("first claim had effects before its publication revalidation")
	}
	switch change {
	case "deleted_mailbox":
		return a.relay.DeleteMailbox(ctx, a.mailbox())
	case "blocked_inviter":
		return publicationDelete(ctx, f, "/v1/admin/inviters/"+publicKey(a.routingPublic()), operator)
	case "revoked_invite":
		return publicationDelete(ctx, f, "/v1/invites/"+entry.Descriptor.InviteID, routePrivate(a.state.RoutingSeed))
	case "local_expiry", "expiry_after_revalidation":
		// Advance only this synthetic local deadline; never edit signed packet
		// bytes or the relay clock/configuration to make an expired claim valid.
		a.mu.Lock()
		entry.Descriptor.ExpiresAt = unixNow() - 1
		a.mu.Unlock()
	}
	return nil
}

func publicationDelete(ctx context.Context, f *selfServiceE2ERelay, path string, signer ed25519.PrivateKey) error {
	r, err := http.NewRequestWithContext(ctx, "DELETE", f.server.URL+path, nil)
	if err != nil {
		return err
	}
	now := time.Now()
	if err = auth.Sign(r, nil, signer, now, now.Add(30*time.Second), randomID()); err != nil {
		return err
	}
	response, err := f.server.Client().Do(r)
	if err != nil {
		return err
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("real signed fixture deletion refused")
	}
	return errors.Join(readErr, closeErr)
}

func TestInvitePublicationTerminalAndExpiredLifecycleClearsConfirmation(t *testing.T) {
	parallelDaemonTest(t)
	f := newSelfServiceHTTPPeer(t, `"self_service"`)
	d := openSelfServiceDaemon(t, f, nil)
	if err := requestSelfServiceInvite(t, d); err != nil {
		t.Fatal("initial publication failed:", safeCode(err))
	}
	entry := publicationEntry(t, d)
	entry.Descriptor.ExpiresAt = unixNow() - 1
	if err := d.syncInvites(t.Context()); safeCode(err) != "expired" || !entry.Terminal || entry.publication != (invitePublication{}) {
		t.Fatal("expired invitation retained publication confirmation")
	}
	assertSelfServiceRetry(t, f, 1, 1)
	if err := d.Sync(t.Context()); err != nil || len(d.state.Issued) != 0 {
		t.Fatal("expired terminal invitation outlived its normal pruning lifecycle")
	}
	value, err := d.Dispatch(t.Context(), localipc.Request{Method: "status", Session: d.state.Label})
	status, ok := value.(Status)
	if err != nil || !ok || len(status.OnboardingErrors) != 0 {
		t.Fatal("pruned terminal invitation retained stale public diagnostics")
	}
}
