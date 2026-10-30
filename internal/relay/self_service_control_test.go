package relay

import (
	"crypto/ed25519"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/relay/auth"
	"github.com/temirlankasen/peerturn/internal/relay/store"
)

func TestSelfServiceHTTPDeniedInviteRevokeCannotSpendReplayStorage(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, nil)
	i := f.invitation(t, time.Minute)
	path := "/v1/invites/" + i.id
	f.request(t, "DELETE", path, nil, nil, 401)
	for _, signer := range []ed25519.PrivateKey{f.publisher, f.admin, i.proof} {
		f.request(t, "DELETE", path, nil, signer, 410)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM nonces"); n != 0 {
		t.Fatal("unknown public revoke allocated durable replay metadata")
	}
	f.publish(t, f.publisher, i, 201)
	before := f.count(t, "SELECT COUNT(*) FROM nonces")
	for _, signer := range []ed25519.PrivateKey{f.stranger, i.proof} {
		f.request(t, "DELETE", path, nil, signer, 403)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM nonces"); n != before {
		t.Fatal("unauthorized signer spent retained author's replay reserve")
	}
	f.request(t, "GET", path, nil, f.publisher, 200)
	f.wantState(t, selfServiceHTTPState{1, 1, 1})
	f.request(t, "DELETE", path, nil, f.publisher, 200)
	if n := f.count(t, "SELECT COUNT(*) FROM nonces"); n != before+1 {
		t.Fatal("successful revoke did not commit durable replay protection")
	}
}

func TestSelfServiceHTTPBlockUnknownInviterReturnsNotFoundWithoutAdmission(t *testing.T) {
	f := newSelfServiceHTTPFixture(t, nil)
	path := "/v1/admin/inviters/" + public(f.stranger)
	f.request(t, "DELETE", path, nil, f.publisher, 403)
	body := f.request(t, "DELETE", path, nil, f.admin, 404)
	if string(body) != `{"error":"not_found"}` {
		t.Fatalf("unknown inviter lost typed refusal: %s", body)
	}
	f.wantState(t, selfServiceHTTPState{})
	f.restart(t)
	f.publish(t, f.stranger, f.invitation(t, time.Minute), 201)
	f.wantState(t, selfServiceHTTPState{1, 1, 1})
}

func TestSelfServiceHTTPWrappedStoreErrorsRemainTypedAndSanitized(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want int
		code string
	}{
		{"missing inviter", &store.Error{Code: "not_found", Status: 404}, 404, "not_found"},
		{"missing invite", &store.Error{Code: "gone", Status: 410}, 410, "gone"},
		{"quota", &store.Error{Code: "quota", Status: 429}, 429, "quota"},
		{"wrong status", &store.Error{Code: "gone", Status: 200}, 503, "storage_unavailable"},
		{"unknown category", &store.Error{Code: "private-storage-detail", Status: 500}, 503, "storage_unavailable"},
		{"untyped", errors.New("private-storage-detail"), 503, "storage_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			storageError(w, errors.Join(auth.ErrUnavailable, test.err))
			if w.Code != test.want || w.Body.String() != `{"error":"`+test.code+`"}` || strings.Contains(w.Body.String(), "private-storage-detail") {
				t.Fatalf("unsafe store error response: %d %s", w.Code, w.Body.String())
			}
			if test.want == 429 || test.want == 503 {
				if w.Header().Get("Retry-After") != "1" {
					t.Fatal("transient error omitted bounded retry hint")
				}
			}
		})
	}
}
