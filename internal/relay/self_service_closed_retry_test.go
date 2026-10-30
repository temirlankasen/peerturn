package relay

import (
	"bytes"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/relay/config"
)

func selfServiceCloseAdmission(t *testing.T, f *selfServiceHTTPFixture) {
	t.Helper()
	f.cfg.AdmissionMode = config.AdmissionClosed
	f.restart(t)
}

func selfServicePublisher(f *selfServiceHTTPFixture, admin bool) ed25519.PrivateKey {
	if admin {
		return f.admin
	}
	return f.publisher
}

func TestSelfServiceHTTPClosedRetryPreservesPublishedInvitationAndClaim(t *testing.T) {
	for _, actor := range []string{"ordinary", "admin"} {
		t.Run(actor, func(t *testing.T) {
			f := newSelfServiceHTTPFixture(t, nil)
			owner := selfServicePublisher(f, actor == "admin")
			i := f.invitation(t, 10*time.Minute)
			created := f.now().UnixNano()
			first := f.publish(t, owner, i, 201)
			claim := i.claim(t, f.recipient)
			f.advance(time.Second)
			selfServiceCloseAdmission(t, f)
			if again := f.publish(t, owner, i, 201); !bytes.Equal(first, again) {
				t.Fatal("mode change altered the immutable committed publication receipt")
			}
			for range 2 {
				f.request(t, "POST", "/v1/invites/"+i.id+"/claims", claim, f.recipient, 200)
			}
			response := f.request(t, "GET", "/v1/invites/"+i.id, nil, i.proof, 200)
			if !bytes.Contains(response, []byte(`"claim_packet"`)) {
				t.Fatal("closed deployment lost retrieval of the already published invitation's claim")
			}
			f.restart(t)
			f.publish(t, owner, i, 201)
			f.request(t, "POST", "/v1/mailboxes", map[string]string{
				"id": selfServiceMailbox(f.recipient), "read_key": public(f.recipient), "voucher": i.voucher,
			}, f.recipient, 201)
			f.wantState(t, selfServiceHTTPState{1, 2, 1})
			if n := f.count(t, "SELECT COUNT(*) FROM invites WHERE id=? AND inviter_key=? AND created_ns=? AND revoked=0", i.id, public(owner), created); n != 1 {
				t.Fatal("closed retry replaced retained author/time or invitation state")
			}
		})
	}
}

func TestSelfServiceHTTPClosedRetryCannotCreateOrAdoptInvitations(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, nil)
	i := f.invitation(t, 10*time.Minute)
	f.publish(t, f.publisher, i, 201)
	selfServiceCloseAdmission(t, f)
	f.publish(t, f.publisher, f.invitation(t, time.Minute), 403)
	f.publish(t, f.stranger, f.invitation(t, time.Minute), 403)
	for _, signer := range []ed25519.PrivateKey{f.stranger, i.proof} {
		f.publish(t, signer, i, 403)
	}
	f.wantState(t, selfServiceHTTPState{1, 1, 1})
	legacy := f.invitation(t, time.Hour)
	f.publish(t, f.admin, legacy, 201)
	f.publish(t, f.publisher, legacy, 403)
	f.wantState(t, selfServiceHTTPState{1, 1, 2})
	if n := f.count(t, "SELECT COUNT(*) FROM invites WHERE id=? AND inviter_key IS NULL AND created_ns IS NULL", legacy.id); n != 1 {
		t.Fatal("closed operator provisioning lost the explicit legacy boundary")
	}
}

func TestSelfServiceHTTPClosedRetryRejectsImmutableDescriptorMutation(t *testing.T) {
	for _, actor := range []string{"ordinary", "admin"} {
		for _, field := range []string{"expiry extension", "packet", "proof key", "voucher"} {
			t.Run(actor+"/"+field, func(t *testing.T) {
				f := newSelfServiceHTTPFixture(t, nil)
				owner := selfServicePublisher(f, actor == "admin")
				i := f.invitation(t, 599*time.Second)
				f.publish(t, owner, i, 201)
				selfServiceCloseAdmission(t, f)
				input := i.input()
				switch field {
				case "expiry extension":
					input["expires_at"] = i.expires + 3600
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
				f.request(t, "POST", "/v1/invites/"+i.id, input, owner, 409)
				f.publish(t, owner, i, 201)
				f.wantState(t, selfServiceHTTPState{1, 1, 1})
				if n := f.count(t, "SELECT COUNT(*) FROM invites WHERE id=? AND expires_ns=? AND inviter_key=?", i.id, time.Unix(i.expires, 0).UnixNano(), public(owner)); n != 1 {
					t.Fatal("closed retry changed expiry or ownership")
				}
			})
		}
	}
}

func TestSelfServiceHTTPClosedRetryPreservesBlockTTLRevokeAndMailboxFences(t *testing.T) {
	for _, actor := range []string{"ordinary", "admin"} {
		for _, state := range []string{"blocked", "expired", "revoked", "deleted mailbox"} {
			t.Run(actor+"/"+state, func(t *testing.T) {
				f := newSelfServiceHTTPFixture(t, nil)
				owner := selfServicePublisher(f, actor == "admin")
				i := f.invitation(t, time.Minute)
				f.publish(t, owner, i, 201)
				want := 410
				switch state {
				case "blocked":
					f.request(t, "DELETE", "/v1/admin/inviters/"+public(owner), nil, f.admin, 200)
					want = 403
				case "revoked":
					f.request(t, "DELETE", "/v1/invites/"+i.id, nil, owner, 200)
				case "deleted mailbox":
					f.request(t, "DELETE", "/v1/mailboxes/"+selfServiceMailbox(owner), nil, owner, 200)
					want = 403
				}
				selfServiceCloseAdmission(t, f)
				if state == "expired" {
					f.advance(61 * time.Second)
				}
				f.publish(t, owner, i, want)
				if state == "deleted mailbox" {
					f.wantState(t, selfServiceHTTPState{1, 0, 1})
				} else {
					f.request(t, "POST", "/v1/invites/"+i.id+"/claims", i.claim(t, f.recipient), f.recipient, 410)
					f.request(t, "DELETE", "/v1/invites/"+i.id, nil, owner, 200)
					f.wantState(t, selfServiceHTTPState{1, 1, 1})
				}
			})
		}
	}
}
