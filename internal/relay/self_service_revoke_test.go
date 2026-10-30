package relay

import (
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/relay/config"
)

func TestSelfServiceHTTPInviteRevokeRequiresAuthorOrAdmin(t *testing.T) {
	for _, actor := range []string{"author", "admin"} {
		t.Run(actor, func(t *testing.T) {
			f := newSelfServiceHTTPFixture(t, nil)
			i := f.invitation(t, 10*time.Minute)
			f.publish(t, f.publisher, i, 201)
			for _, stranger := range []ed25519.PrivateKey{f.stranger, i.proof} {
				f.request(t, "DELETE", "/v1/invites/"+i.id, nil, stranger, 403)
				f.request(t, "GET", "/v1/invites/"+i.id, nil, f.publisher, 200)
				if n := f.count(t, "SELECT COUNT(*) FROM invites WHERE id=? AND revoked=0", i.id); n != 1 {
					t.Fatal("non-owner signature revoked an invitation")
				}
			}
			signer := f.publisher
			if actor == "admin" {
				signer = f.admin
			}
			for range 2 {
				f.request(t, "DELETE", "/v1/invites/"+i.id, nil, signer, 200)
			}
			f.request(t, "GET", "/v1/invites/"+i.id, nil, f.publisher, 410)
			f.request(t, "POST", "/v1/invites/"+i.id+"/claims", i.claim(t, f.recipient), f.recipient, 410)
			f.wantState(t, selfServiceHTTPState{1, 1, 1})
		})
	}
}

func TestSelfServiceHTTPUnknownInviteDeleteCannotPreemptPublication(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, nil)
	i := f.invitation(t, time.Minute)
	for _, signer := range []ed25519.PrivateKey{f.publisher, f.admin, i.proof} {
		f.request(t, "DELETE", "/v1/invites/"+i.id, nil, signer, 410)
		f.wantState(t, selfServiceHTTPState{})
	}
	f.publish(t, f.stranger, i, 201)
	f.request(t, "DELETE", "/v1/invites/"+i.id, nil, f.publisher, 403)
	f.request(t, "GET", "/v1/invites/"+i.id, nil, f.stranger, 200)
}

func TestSelfServiceHTTPExpiredInviteRetainsAuthorRevokeAuthority(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, nil)
	i := f.invitation(t, time.Second)
	f.publish(t, f.publisher, i, 201)
	f.advance(2 * time.Second)
	f.request(t, "GET", "/v1/invites/"+i.id, nil, f.publisher, 410)
	f.request(t, "DELETE", "/v1/invites/"+i.id, nil, i.proof, 403)
	f.request(t, "DELETE", "/v1/invites/"+i.id, nil, f.publisher, 200)
	f.wantState(t, selfServiceHTTPState{1, 1, 1})
}

func TestSelfServiceHTTPClosedLegacyInviteRevokeIsAdminOnly(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, func(c *config.Config) { c.AdmissionMode = config.AdmissionClosed })
	i := f.invitation(t, time.Hour)
	f.publish(t, f.admin, i, 201)
	for _, signer := range []ed25519.PrivateKey{f.publisher, i.proof} {
		f.request(t, "DELETE", "/v1/invites/"+i.id, nil, signer, 403)
	}
	f.request(t, "DELETE", "/v1/invites/"+i.id, nil, f.admin, 200)
	f.request(t, "GET", "/v1/invites/"+i.id, nil, f.publisher, 410)
	f.wantState(t, selfServiceHTTPState{0, 0, 1})
}
