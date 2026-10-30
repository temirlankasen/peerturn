package relay

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/relay/config"
)

func selfServiceOwnReceipt(t *testing.T, data []byte, id string, signer ed25519.PrivateKey) {
	t.Helper()
	var receipt struct {
		ID      string `json:"id"`
		Mailbox string `json:"mailbox"`
	}
	if err := json.Unmarshal(data, &receipt); err != nil || receipt.ID != id || receipt.Mailbox != selfServiceMailbox(signer) {
		t.Fatalf("publication did not acknowledge verified signer's own mailbox: %s, %v", data, err)
	}
}

func selfServiceLegacyReceipt(t *testing.T, data []byte, id string) {
	t.Helper()
	var receipt map[string]string
	if err := json.Unmarshal(data, &receipt); err != nil || len(receipt) != 1 || receipt["id"] != id {
		t.Fatalf("closed legacy publication changed its id-only receipt: %s, %v", data, err)
	}
}

func TestSelfServiceHTTPReceiptAcknowledgesVerifiedSignerForEveryPublisher(t *testing.T) {
	for _, actor := range []string{"ordinary", "admin"} {
		for _, flag := range []string{"absent", "false", "true"} {
			t.Run(actor+"/"+flag, func(t *testing.T) {
				f := newSelfServiceHTTPFixture(t, nil)
				owner := selfServicePublisher(f, actor == "admin")
				i := f.invitation(t, 10*time.Minute)
				input := i.input()
				if flag != "absent" {
					input["require_own_mailbox"] = flag == "true"
				}
				data := f.request(t, "POST", "/v1/invites/"+i.id, input, owner, 201)
				selfServiceOwnReceipt(t, data, i.id, owner)
				if bytes.Contains(data, []byte(i.voucher)) || bytes.Contains(data, []byte(selfServiceMailbox(i.proof))) {
					t.Fatal("receipt leaked the voucher or acknowledged the invitation proof key as owner")
				}
				f.wantState(t, selfServiceHTTPState{1, 1, 1})
				if n := f.count(t, "SELECT COUNT(*) FROM mailboxes WHERE id=? AND read_key=?", selfServiceMailbox(owner), public(owner)); n != 1 {
					t.Fatal("successful own-mailbox receipt was not backed by signer-bound custody")
				}
			})
		}
	}
}

func TestSelfServiceHTTPReceiptStaleOpenHintCannotSatisfyClosedRequirement(t *testing.T) {
	for _, actor := range []string{"ordinary", "admin"} {
		t.Run(actor, func(t *testing.T) {
			f := newSelfServiceHTTPFixture(t, nil)
			owner := selfServicePublisher(f, actor == "admin")
			hint := f.request(t, "GET", "/.well-known/peerturn", nil, nil, 200)
			if !bytes.Contains(hint, []byte(`"admission_mode":"self_service"`)) {
				t.Fatal("fixture did not observe the stale open-mode hint")
			}
			i := f.invitation(t, time.Minute)
			input := i.input()
			input["require_own_mailbox"] = true
			// The fully signed request crosses the operator's mode switch.
			r := f.signed(t, "POST", "/v1/invites/"+i.id, selfServiceJSON(t, input), owner)
			selfServiceCloseAdmission(t, f)
			f.serve(t, r, 403)
			f.wantState(t, selfServiceHTTPState{})
			if actor == "admin" {
				selfServiceLegacyReceipt(t, f.publish(t, owner, i, 201), i.id)
				f.wantState(t, selfServiceHTTPState{0, 0, 1})
			}
		})
	}
}

func TestSelfServiceHTTPReceiptStaleClosedHintCannotOverrideEnabledPolicy(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, func(c *config.Config) { c.AdmissionMode = config.AdmissionClosed })
	hint := f.request(t, "GET", "/.well-known/peerturn", nil, nil, 200)
	if bytes.Contains(hint, []byte(`"admission_mode"`)) {
		t.Fatal("closed fixture unexpectedly advertised open admission")
	}
	i := f.invitation(t, time.Minute)
	r := f.signed(t, "POST", "/v1/invites/"+i.id, selfServiceJSON(t, i.input()), f.admin)
	f.cfg.AdmissionMode = config.AdmissionSelfService
	f.restart(t)
	data := f.serve(t, r, 201).Body.Bytes()
	selfServiceOwnReceipt(t, data, i.id, f.admin)
	f.wantState(t, selfServiceHTTPState{1, 1, 1})
}

func TestSelfServiceHTTPReceiptClosedExactOwnedRetryCanRequireMailbox(t *testing.T) {
	for _, actor := range []string{"ordinary", "admin"} {
		t.Run(actor, func(t *testing.T) {
			f := newSelfServiceHTTPFixture(t, nil)
			owner := selfServicePublisher(f, actor == "admin")
			i := f.invitation(t, time.Minute)
			f.publish(t, owner, i, 201)
			selfServiceCloseAdmission(t, f)
			input := i.input()
			// This transient response precondition is not a new invite descriptor field.
			input["require_own_mailbox"] = true
			for range 2 {
				data := f.request(t, "POST", "/v1/invites/"+i.id, input, owner, 201)
				selfServiceOwnReceipt(t, data, i.id, owner)
				f.restart(t)
			}
			f.wantState(t, selfServiceHTTPState{1, 1, 1})
		})
	}
}

func TestSelfServiceHTTPReceiptClosedLegacyCannotSatisfyOwnRequirement(t *testing.T) {
	for _, existingMailbox := range []bool{false, true} {
		name := "fresh admin"
		if existingMailbox {
			name = "admin with provisioned own mailbox"
		}
		t.Run(name, func(t *testing.T) {
			f := newSelfServiceHTTPFixture(t, func(c *config.Config) { c.AdmissionMode = config.AdmissionClosed })
			if existingMailbox {
				f.request(t, "POST", "/v1/mailboxes", map[string]string{
					"id": selfServiceMailbox(f.admin), "read_key": public(f.admin),
				}, f.admin, 201)
			}
			i := f.invitation(t, time.Hour)
			before := f.state(t)
			input := i.input()
			input["require_own_mailbox"] = true
			f.request(t, "POST", "/v1/invites/"+i.id, input, f.admin, 403)
			f.wantState(t, before)
			selfServiceLegacyReceipt(t, f.publish(t, f.admin, i, 201), i.id)
			input["require_own_mailbox"] = false
			data := f.request(t, "POST", "/v1/invites/"+i.id, input, f.admin, 201)
			selfServiceLegacyReceipt(t, data, i.id)
			before = f.state(t)
			input["require_own_mailbox"] = true
			f.request(t, "POST", "/v1/invites/"+i.id, input, f.admin, 403)
			f.wantState(t, before)
		})
	}
}

func TestSelfServiceHTTPReceiptRequiresStrictSignedBoolean(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
	}{
		{"null", nil}, {"string", "true"}, {"number", 1},
		{"array", []bool{true}}, {"object", map[string]bool{"value": true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSelfServiceHTTPFixture(t, nil)
			i := f.invitation(t, time.Minute)
			input := i.input()
			input["require_own_mailbox"] = test.value
			f.request(t, "POST", "/v1/invites/"+i.id, input, f.publisher, 400)
			f.wantState(t, selfServiceHTTPState{})
		})
	}
	f := newSelfServiceHTTPFixture(t, nil)
	i := f.invitation(t, time.Minute)
	input := i.input()
	input["require_own_mailbox"] = false
	r := f.signed(t, "POST", "/v1/invites/"+i.id, selfServiceJSON(t, input), f.publisher)
	input["require_own_mailbox"] = true
	changed := selfServiceJSON(t, input)
	r.Body = io.NopCloser(bytes.NewReader(changed))
	r.ContentLength = int64(len(changed))
	f.serve(t, r, 401)
	f.wantState(t, selfServiceHTTPState{})
	if n := f.count(t, "SELECT COUNT(*) FROM nonces"); n != 0 {
		t.Fatal("tampered response requirement consumed durable replay storage")
	}
}

func TestSelfServiceHTTPReceiptRequirementCannotBypassClosedTerminalFences(t *testing.T) {
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
				before := f.state(t)
				input := i.input()
				input["require_own_mailbox"] = true
				f.request(t, "POST", "/v1/invites/"+i.id, input, owner, want)
				f.wantState(t, before)
			})
		}
	}
}
