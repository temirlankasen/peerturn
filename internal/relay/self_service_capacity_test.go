package relay

import (
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/relay/config"
)

func selfServicePrepareRecovery(t *testing.T, f *selfServiceHTTPFixture) {
	t.Helper()
	f.request(t, "POST", "/v1/mailboxes", map[string]string{"id": "recovery", "read_key": public(f.recipient)}, f.admin, 201)
	f.request(t, "PUT", "/v1/mailboxes/recovery/permits/write", map[string]any{
		"writer_key": public(f.stranger), "epoch": 1, "expires_at": f.now().Add(time.Hour).Unix(),
	}, f.recipient, 200)
	p := packet(t, 1, "synthetic recovery fixture")
	p.Header.DstMailbox, p.Header.ExpiresAt = "recovery", f.now().Add(time.Hour).Unix()
	f.request(t, "POST", "/v1/mailboxes/recovery/messages", p, f.stranger, 200)
}

func selfServiceRecover(t *testing.T, f *selfServiceHTTPFixture) {
	t.Helper()
	f.request(t, "GET", "/v1/mailboxes/recovery/messages?limit=1", nil, f.recipient, 200)
	f.request(t, "POST", "/v1/mailboxes/recovery/ack", map[string]int{"through": 1}, f.recipient, 200)
	f.request(t, "DELETE", "/v1/mailboxes/recovery/permits/write", map[string]int{"epoch": 2}, f.recipient, 200)
	f.request(t, "DELETE", "/v1/mailboxes/recovery", nil, f.recipient, 200)
}

func TestSelfServiceHTTPBootstrapBurstCannotSpendControlOrRecoveryCapacity(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, nil)
	selfServicePrepareRecovery(t, f)
	var owners []ed25519.PrivateKey
	var invitations []selfServiceHTTPInvite
	// Freeze the server clock so the existing 16-token anonymous burst cannot refill.
	for range 16 {
		owner, i := key(t), f.invitation(t, 10*time.Minute)
		f.publish(t, owner, i, 201)
		owners, invitations = append(owners, owner), append(invitations, i)
	}
	before := f.state(t)
	f.publish(t, key(t), f.invitation(t, 10*time.Minute), 429)
	f.wantState(t, before)
	f.request(t, "DELETE", "/v1/invites/"+invitations[0].id, nil, owners[0], 200)
	f.request(t, "DELETE", "/v1/admin/inviters/"+public(owners[1]), nil, f.admin, 200)
	selfServiceRecover(t, f)
}

func TestSelfServiceHTTPPublisherNonceLaneCannotSpendItsControlReserve(t *testing.T) {
	for _, adminPublisher := range []bool{false, true} {
		name := "publisher"
		if adminPublisher {
			name = "admin publisher"
		}
		t.Run(name, func(t *testing.T) {
			f := newSelfServiceHTTPFixture(t, nil)
			publisher := f.publisher
			if adminPublisher {
				publisher = f.admin
			}
			i := f.invitation(t, 10*time.Minute)
			// The bounded bootstrap replay lane admits at most eight nonces per key.
			for range 8 {
				f.publish(t, publisher, i, 201)
			}
			f.publish(t, publisher, i, 429)
			f.wantState(t, selfServiceHTTPState{1, 1, 1})
			f.request(t, "DELETE", "/v1/invites/"+i.id, nil, publisher, 200)
			f.request(t, "DELETE", "/v1/admin/inviters/"+public(publisher), nil, f.admin, 200)
		})
	}
}

func TestSelfServiceHTTPQuotaExhaustionPreservesOwnerRecovery(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, func(c *config.Config) {
		c.SelfService.ActiveInvites, c.SelfService.Inviters = 1, 1
	})
	selfServicePrepareRecovery(t, f)
	i := f.invitation(t, 10*time.Minute)
	f.publish(t, f.publisher, i, 201)
	f.publish(t, f.publisher, f.invitation(t, 10*time.Minute), 429)
	f.publish(t, f.stranger, f.invitation(t, 10*time.Minute), 429)
	if n := f.count(t, "SELECT COUNT(*) FROM relay_inviters"); n != 1 {
		t.Fatal("quota refusal admitted another routing key")
	}
	f.request(t, "DELETE", "/v1/invites/"+i.id, nil, f.publisher, 200)
	f.request(t, "DELETE", "/v1/admin/inviters/"+public(f.publisher), nil, f.admin, 200)
	selfServiceRecover(t, f)
}
