package daemon

import (
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/invite"
)

func TestTerminalInvitesDoNotExhaustLifetimeBudgetOrRetainSecretURLs(t *testing.T) {
	a, _ := pairedStores(t)
	for n := range 32 {
		id := strings.Repeat("a", n+1)
		a.state.Issued[id] = &issuedInvite{URL: "secret-issued", Terminal: true, Descriptor: invite.Descriptor{ExpiresAt: unixNow() + 300}}
		a.state.Joining[id] = &joiningInvite{URL: "secret-joining", Terminal: true, Descriptor: invite.Descriptor{ExpiresAt: unixNow() + 300}}
	}
	if err := a.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, entry := range a.state.Issued {
		if entry.URL != "" {
			t.Error("terminal issued URL retained")
		}
	}
	for _, entry := range a.state.Joining {
		if entry.URL != "" {
			t.Error("terminal join URL retained")
		}
	}
	recoveryRelayOffline(t, a)
	if _, err := a.createInvite(t.Context()); fault.IsCode(err, fault.BudgetExceeded) {
		t.Fatal("completed invitations consumed active budget")
	}
}
