package relay

import (
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/relay/config"
)

func TestSelfServiceHTTPVoucherNamespaceRejectsVictimAddressAndKeepsVoucher(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, nil)
	i := f.invitation(t, 10*time.Minute)
	f.publish(t, f.publisher, i, 201)
	f.request(t, "POST", "/v1/invites/"+i.id+"/claims", i.claim(t, f.recipient), f.recipient, 200)
	input := map[string]string{"id": selfServiceMailbox(f.stranger), "read_key": public(f.recipient), "voucher": i.voucher}
	f.request(t, "POST", "/v1/mailboxes", input, f.recipient, 403)
	f.wantState(t, selfServiceHTTPState{1, 1, 1})
	if n := f.count(t, "SELECT COUNT(*) FROM invites WHERE id=? AND enrolled_mailbox IS NULL", i.id); n != 1 {
		t.Fatal("rejected namespace squat consumed the winning reader's voucher")
	}
	f.restart(t)
	input["id"] = selfServiceMailbox(f.recipient)
	f.request(t, "POST", "/v1/mailboxes", input, f.recipient, 201)
	f.request(t, "POST", "/v1/mailboxes", input, f.recipient, 201)
	f.publish(t, f.stranger, f.invitation(t, time.Minute), 201)
	f.wantState(t, selfServiceHTTPState{2, 3, 2})
	f.request(t, "GET", "/v1/mailboxes/"+selfServiceMailbox(f.stranger)+"/messages", nil, f.stranger, 200)
	f.request(t, "GET", "/v1/mailboxes/"+selfServiceMailbox(f.stranger)+"/messages", nil, f.recipient, 403)
}

func TestSelfServiceHTTPVoucherNamespacePreservesClosedLegacyEnrollment(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, func(c *config.Config) { c.AdmissionMode = config.AdmissionClosed })
	i := f.invitation(t, time.Hour)
	f.publish(t, f.admin, i, 201)
	f.request(t, "POST", "/v1/invites/"+i.id+"/claims", i.claim(t, f.recipient), f.recipient, 200)
	f.request(t, "POST", "/v1/mailboxes", map[string]string{
		"id": "legacy-arbitrary", "read_key": public(f.recipient), "voucher": i.voucher,
	}, f.recipient, 201)
	f.request(t, "POST", "/v1/mailboxes", map[string]string{
		"id": "operator-arbitrary", "read_key": public(f.stranger),
	}, f.admin, 201)
	f.wantState(t, selfServiceHTTPState{0, 2, 1})
	if n := f.count(t, "SELECT COUNT(*) FROM invites WHERE id=? AND inviter_key IS NULL AND created_ns IS NULL", i.id); n != 1 {
		t.Fatal("closed legacy enrollment was reclassified as self-service")
	}
}
