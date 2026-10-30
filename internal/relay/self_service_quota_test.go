package relay

import (
	"bytes"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/relay/config"
)

func TestSelfServiceHTTPQuotaRetryRevokeRollingAndLifetime(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, func(c *config.Config) {
		c.SelfService = config.SelfServiceLimits{ActiveInvites: 1, DailyInvites: 2, LifetimeInvites: 3, Inviters: 100}
	})
	first := f.invitation(t, 10*time.Minute)
	response := f.publish(t, f.publisher, first, 201)
	if again := f.publish(t, f.publisher, first, 201); !bytes.Equal(response, again) {
		t.Fatal("identical retry changed the committed publication result")
	}
	second := f.invitation(t, 10*time.Minute)
	f.publish(t, f.publisher, second, 429)
	f.wantState(t, selfServiceHTTPState{1, 1, 1})
	f.request(t, "DELETE", "/v1/invites/"+first.id, nil, f.publisher, 200)
	f.publish(t, f.publisher, second, 201)
	f.request(t, "DELETE", "/v1/invites/"+second.id, nil, f.publisher, 200)
	f.publish(t, f.publisher, f.invitation(t, time.Minute), 429)
	f.wantState(t, selfServiceHTTPState{1, 1, 2})
	f.restart(t)
	f.publish(t, f.publisher, f.invitation(t, time.Minute), 429)
	f.advance(24*time.Hour + time.Second)
	third := f.invitation(t, 10*time.Minute)
	f.publish(t, f.publisher, third, 201)
	f.request(t, "DELETE", "/v1/invites/"+third.id, nil, f.publisher, 200)
	f.publish(t, f.publisher, f.invitation(t, time.Minute), 429)
	f.advance(24*time.Hour + time.Second)
	f.restart(t)
	f.publish(t, f.publisher, f.invitation(t, time.Minute), 429)
	f.wantState(t, selfServiceHTTPState{1, 1, 3})
}

func TestSelfServiceHTTPFirstPublicationQuotaRollbackAndRetry(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, func(c *config.Config) {
		// Registration, mailbox and invite cannot all fit into two metadata rows.
		c.Limits.MailboxItems, c.Limits.MetadataItems = 1, 2
	})
	i := f.invitation(t, 10*time.Minute)
	f.publish(t, f.publisher, i, 429)
	f.wantState(t, selfServiceHTTPState{})
	f.cfg.Limits.MailboxItems, f.cfg.Limits.MetadataItems = 1000, 100000
	f.restart(t)
	f.publish(t, f.publisher, i, 201)
	f.wantState(t, selfServiceHTTPState{1, 1, 1})
}

func TestSelfServiceHTTPInviterCapIncludesDeletedAndBlockedOwners(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, func(c *config.Config) { c.SelfService.Inviters = 1 })
	f.publish(t, f.publisher, f.invitation(t, 10*time.Minute), 201)
	i := f.invitation(t, 10*time.Minute)
	f.publish(t, f.stranger, i, 429)
	f.wantState(t, selfServiceHTTPState{1, 1, 1})
	f.request(t, "DELETE", "/v1/mailboxes/"+selfServiceMailbox(f.publisher), nil, f.publisher, 200)
	f.request(t, "DELETE", "/v1/admin/inviters/"+public(f.publisher), nil, f.admin, 200)
	f.restart(t)
	f.publish(t, f.stranger, i, 429)
	f.wantState(t, selfServiceHTTPState{1, 0, 1})
}

func TestSelfServiceHTTPConflictingMailboxRollsBackInviterAndInvite(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, nil)
	f.request(t, "POST", "/v1/mailboxes", map[string]string{"id": selfServiceMailbox(f.publisher), "read_key": public(f.stranger)}, f.admin, 201)
	f.publish(t, f.publisher, f.invitation(t, time.Minute), 409)
	f.wantState(t, selfServiceHTTPState{0, 1, 0})
	f.request(t, "GET", "/v1/mailboxes/"+selfServiceMailbox(f.publisher)+"/messages", nil, f.stranger, 200)
	f.request(t, "GET", "/v1/mailboxes/"+selfServiceMailbox(f.publisher)+"/messages", nil, f.publisher, 403)
}

func TestSelfServiceHTTPInviteIDCollisionCannotChangeAuthor(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, nil)
	i := f.invitation(t, time.Minute)
	f.publish(t, f.publisher, i, 201)
	f.publish(t, f.stranger, i, 409)
	f.wantState(t, selfServiceHTTPState{1, 1, 1})
	if n := f.count(t, "SELECT COUNT(*) FROM invites WHERE id=? AND inviter_key=?", i.id, public(f.publisher)); n != 1 {
		t.Fatal("a second signer replaced invitation authorship")
	}
	f.publish(t, f.publisher, i, 201)
}

func TestSelfServiceHTTPClaimedInviteStillConsumesActiveQuota(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, func(c *config.Config) { c.SelfService.ActiveInvites = 1 })
	i := f.invitation(t, 10*time.Minute)
	f.publish(t, f.publisher, i, 201)
	claim := i.claim(t, f.recipient)
	f.request(t, "POST", "/v1/invites/"+i.id+"/claims", claim, f.stranger, 403)
	f.request(t, "POST", "/v1/invites/"+i.id+"/claims", claim, f.recipient, 200)
	f.request(t, "POST", "/v1/mailboxes", map[string]string{
		"id": selfServiceMailbox(f.stranger), "read_key": public(f.stranger), "voucher": i.voucher,
	}, f.stranger, 403)
	f.request(t, "POST", "/v1/mailboxes", map[string]string{
		"id": selfServiceMailbox(f.recipient), "read_key": public(f.recipient), "voucher": i.voucher,
	}, f.recipient, 201)
	f.request(t, "POST", "/v1/mailboxes", map[string]string{
		"id": "second-enrollment", "read_key": public(f.recipient), "voucher": i.voucher,
	}, f.recipient, 403)
	f.publish(t, f.publisher, f.invitation(t, time.Minute), 429)
	f.wantState(t, selfServiceHTTPState{1, 2, 1})
	f.advance(601 * time.Second)
	f.publish(t, f.publisher, f.invitation(t, time.Minute), 201)
	f.wantState(t, selfServiceHTTPState{1, 2, 2})
}

func TestSelfServiceHTTPRetryCannotExtendOrReplaceInvitation(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, nil)
	i := f.invitation(t, 599*time.Second)
	f.publish(t, f.publisher, i, 201)
	for _, name := range []string{"expiry", "packet", "proof key", "voucher"} {
		t.Run(name, func(t *testing.T) {
			input := i.input()
			switch name {
			case "expiry":
				input["expires_at"] = i.expires + 1
			case "packet":
				changed := i.packet
				changed.Ciphertext = append([]byte{}, changed.Ciphertext...)
				changed.Ciphertext[0] ^= 1
				input["packet"] = changed
			case "proof key":
				input["proof_key"] = public(f.stranger)
			case "voucher":
				input["voucher"] = i.voucher + "changed"
			}
			f.request(t, "POST", "/v1/invites/"+i.id, input, f.publisher, 409)
			f.wantState(t, selfServiceHTTPState{1, 1, 1})
		})
	}
	f.publish(t, f.publisher, i, 201)
	f.advance(600 * time.Second)
	f.publish(t, f.publisher, i, 410)
	f.wantState(t, selfServiceHTTPState{1, 1, 1})
}
