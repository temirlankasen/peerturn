package relay

import (
	"crypto/ed25519"
	"testing"
	"time"
)

func TestSelfServiceHTTPBlockInviterEndpointIsAdminOnly(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, nil)
	i := f.invitation(t, 10*time.Minute)
	f.publish(t, f.publisher, i, 201)
	path := "/v1/admin/inviters/" + public(f.publisher)
	f.request(t, "DELETE", path, nil, nil, 401)
	for _, signer := range []ed25519.PrivateKey{f.publisher, f.stranger, i.proof} {
		f.request(t, "DELETE", path, nil, signer, 403)
		if n := f.count(t, "SELECT COUNT(*) FROM relay_inviters WHERE routing_key=? AND revoked=0", public(f.publisher)); n != 1 {
			t.Fatal("non-admin blocked an inviter")
		}
	}
	f.request(t, "GET", "/v1/invites/"+i.id, nil, f.publisher, 200)
	f.request(t, "DELETE", path, nil, f.admin, 200)
}

func TestSelfServiceHTTPDurableBlockFencesPublishClaimAndEnrollment(t *testing.T) {
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
			pending := f.invitation(t, 10*time.Minute)
			claimed := f.invitation(t, 10*time.Minute)
			f.publish(t, publisher, pending, 201)
			f.publish(t, publisher, claimed, 201)
			claim := claimed.claim(t, f.recipient)
			f.request(t, "POST", "/v1/invites/"+claimed.id+"/claims", claim, f.recipient, 200)
			for range 2 {
				f.request(t, "DELETE", "/v1/admin/inviters/"+public(publisher), nil, f.admin, 200)
			}
			if n := f.count(t, "SELECT COUNT(*) FROM relay_inviters WHERE routing_key=? AND revoked=1", public(publisher)); n != 1 {
				t.Fatal("block did not persist the exact inviter")
			}
			if n := f.count(t, "SELECT COUNT(*) FROM invites WHERE inviter_key=? AND revoked=1", public(publisher)); n != 2 {
				t.Fatal("block left an inviter's retained invitation active")
			}
			f.request(t, "DELETE", "/v1/mailboxes/"+selfServiceMailbox(publisher), nil, publisher, 200)
			f.restart(t)
			f.publish(t, publisher, f.invitation(t, time.Minute), 403)
			f.request(t, "POST", "/v1/invites/"+pending.id+"/claims", pending.claim(t, f.recipient), f.recipient, 410)
			f.request(t, "POST", "/v1/invites/"+claimed.id+"/claims", claim, f.recipient, 410)
			f.request(t, "POST", "/v1/mailboxes", map[string]string{
				"id": "blocked-enrollment", "read_key": public(f.recipient), "voucher": claimed.voucher,
			}, f.recipient, 403)
			f.wantState(t, selfServiceHTTPState{1, 0, 2})
			// Blocking one routing key must not turn off the opted-in deployment.
			f.publish(t, f.stranger, f.invitation(t, time.Minute), 201)
		})
	}
}
