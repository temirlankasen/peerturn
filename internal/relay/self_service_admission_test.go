package relay

import (
	"bytes"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/relay/config"
)

func TestSelfServiceHTTPFreshSignedPublisherCreatesOnlyOwnMailbox(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, nil)
	i := f.invitation(t, 10*time.Minute)
	response := f.publish(t, f.publisher, i, 201)
	if bytes.Contains(response, []byte(i.voucher)) {
		t.Fatal("publication response exposed the private voucher")
	}
	f.wantState(t, selfServiceHTTPState{1, 1, 1})
	if n := f.count(t, "SELECT COUNT(*) FROM mailboxes WHERE id=? AND read_key=?", selfServiceMailbox(f.publisher), public(f.publisher)); n != 1 {
		t.Fatal("publication did not bind exactly the derived signer mailbox")
	}
	if n := f.count(t, "SELECT COUNT(*) FROM invites WHERE id=? AND inviter_key=? AND created_ns=?", i.id, public(f.publisher), f.now().UnixNano()); n != 1 {
		t.Fatal("invitation author or creation time did not come from verified signer and relay clock")
	}
	f.request(t, "GET", "/v1/mailboxes/"+selfServiceMailbox(f.publisher)+"/messages", nil, f.publisher, 200)
	f.request(t, "GET", "/v1/mailboxes/"+selfServiceMailbox(f.publisher)+"/messages", nil, f.stranger, 403)
	for _, signer := range []ed25519.PrivateKey{f.publisher, f.stranger, i.proof} {
		f.request(t, "POST", "/v1/mailboxes", map[string]string{"id": "arbitrary", "read_key": public(signer)}, signer, 403)
	}
	f.wantState(t, selfServiceHTTPState{1, 1, 1})
}

func TestSelfServiceHTTPClosedPreservesLegacyAdminPublicationAndTTL(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, func(c *config.Config) {
		c.AdmissionMode = config.Defaults().AdmissionMode
		c.SelfService = config.SelfServiceLimits{ActiveInvites: 1, DailyInvites: 1, LifetimeInvites: 1, Inviters: 1}
	})
	i := f.invitation(t, time.Hour)
	f.publish(t, f.publisher, i, 403)
	f.wantState(t, selfServiceHTTPState{})
	first := f.publish(t, f.admin, i, 201)
	if again := f.publish(t, f.admin, i, 201); !bytes.Equal(first, again) {
		t.Fatal("legacy admin retry changed its publication result")
	}
	f.publish(t, f.admin, f.invitation(t, time.Hour), 201)
	f.wantState(t, selfServiceHTTPState{0, 0, 2})
	if n := f.count(t, "SELECT COUNT(*) FROM invites WHERE inviter_key IS NULL AND created_ns IS NULL"); n != 2 {
		t.Fatal("closed admin publication was silently promoted into self-service admission")
	}
	f.request(t, "POST", "/v1/mailboxes", map[string]string{"id": "operator-box", "read_key": public(f.recipient)}, f.admin, 201)
	f.request(t, "POST", "/v1/mailboxes", map[string]string{"id": "public-box", "read_key": public(f.publisher)}, f.publisher, 403)
}

func TestSelfServiceHTTPAllPublishersHaveSameTTLBound(t *testing.T) {
	for _, admin := range []bool{false, true} {
		for _, test := range []struct {
			name string
			ttl  time.Duration
			want int
		}{
			{"expired", -time.Second, 400},
			{"now", 0, 400},
			{"minimum", time.Second, 201},
			{"maximum", 600 * time.Second, 201},
			{"over maximum", 601 * time.Second, 400},
			{"legacy long TTL", time.Hour, 400},
		} {
			name := "publisher/"
			if admin {
				name = "admin/"
			}
			t.Run(name+test.name, func(t *testing.T) {
				f := newSelfServiceHTTPFixture(t, nil)
				signer := f.publisher
				if admin {
					signer = f.admin
				}
				f.publish(t, signer, f.invitation(t, test.ttl), test.want)
				if test.want == 201 {
					f.wantState(t, selfServiceHTTPState{1, 1, 1})
				} else {
					f.wantState(t, selfServiceHTTPState{})
				}
			})
		}
	}
}

func TestSelfServiceHTTPAdminUsesOwnBoundedPublication(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, func(c *config.Config) {
		c.SelfService.ActiveInvites = 1
	})
	i := f.invitation(t, time.Minute)
	f.publish(t, f.admin, i, 201)
	f.publish(t, f.admin, i, 201)
	f.publish(t, f.admin, f.invitation(t, time.Minute), 429)
	f.wantState(t, selfServiceHTTPState{1, 1, 1})
	if n := f.count(t, "SELECT COUNT(*) FROM invites WHERE inviter_key=?", public(f.admin)); n != 1 {
		t.Fatal("admin publication bypassed retained authorship")
	}
	f.request(t, "GET", "/v1/mailboxes/"+selfServiceMailbox(f.admin)+"/messages", nil, f.admin, 200)
}
