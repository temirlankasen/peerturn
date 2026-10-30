package relay

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/invite"
)

func TestSelfServiceHTTPMalformedSignedPublicationLeavesNoAdmissionRows(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any, *selfServiceHTTPInvite)
		want   int
	}{
		{"unknown author field", func(v map[string]any, i *selfServiceHTTPInvite) { v["inviter_key"] = public(i.proof) }, 400},
		{"client creation time", func(v map[string]any, _ *selfServiceHTTPInvite) { v["created_ns"] = 1 }, 400},
		{"arbitrary mailbox", func(v map[string]any, _ *selfServiceHTTPInvite) { v["mailbox"] = "arbitrary" }, 400},
		{"arbitrary reader", func(v map[string]any, i *selfServiceHTTPInvite) { v["read_key"] = public(i.proof) }, 400},
		{"invalid proof key", func(v map[string]any, _ *selfServiceHTTPInvite) { v["proof_key"] = "invalid" }, 400},
		{"missing voucher", func(v map[string]any, _ *selfServiceHTTPInvite) { delete(v, "voucher") }, 400},
		{"wrong expiry type", func(v map[string]any, _ *selfServiceHTTPInvite) { v["expires_at"] = "600" }, 400},
		{"wrong packet ID", func(v map[string]any, i *selfServiceHTTPInvite) {
			p := i.packet
			p.InviteID = opaqueID(t)
			v["packet"] = p
		}, 400},
		{"bad packet", func(v map[string]any, _ *selfServiceHTTPInvite) { v["packet"] = invite.Packet{} }, 426},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSelfServiceHTTPFixture(t, nil)
			i := f.invitation(t, time.Minute)
			input := i.input()
			test.mutate(input, &i)
			f.request(t, "POST", "/v1/invites/"+i.id, input, f.publisher, test.want)
			f.wantState(t, selfServiceHTTPState{})
		})
	}
	for _, body := range [][]byte{nil, []byte("{"), []byte("{}"), []byte("[]"), []byte("null")} {
		t.Run("raw/"+string(body), func(t *testing.T) {
			f := newSelfServiceHTTPFixture(t, nil)
			r := f.signed(t, "POST", "/v1/invites/"+opaqueID(t), body, f.publisher)
			f.serve(t, r, 400)
			f.wantState(t, selfServiceHTTPState{})
		})
	}
}

func TestSelfServiceHTTPUnsignedOrTamperedPublisherCannotAdmit(t *testing.T) {
	for _, name := range []string{"unsigned", "changed body", "changed target"} {
		t.Run(name, func(t *testing.T) {
			f := newSelfServiceHTTPFixture(t, nil)
			i := f.invitation(t, time.Minute)
			body := selfServiceJSON(t, i.input())
			signer := f.publisher
			if name == "unsigned" {
				signer = nil
			}
			r := f.signed(t, "POST", "/v1/invites/"+i.id, body, signer)
			switch name {
			case "changed body":
				body = append(body, ' ')
				r.Body = io.NopCloser(bytes.NewReader(body))
				r.ContentLength = int64(len(body))
			case "changed target":
				r.URL.Path = "/v1/invites/" + opaqueID(t)
				r.RequestURI = r.URL.RequestURI()
			}
			f.serve(t, r, 401)
			f.wantState(t, selfServiceHTTPState{})
			if n := f.count(t, "SELECT COUNT(*) FROM nonces"); n != 0 {
				t.Fatal("unverified request wrote durable replay state")
			}
			f.publish(t, f.publisher, i, 201)
		})
	}
}
