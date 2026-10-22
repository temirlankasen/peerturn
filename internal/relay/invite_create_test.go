package relay

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/invite"
)

func TestInviteCreateRequiresAdminAndKeepsVoucherPrivate(t *testing.T) {
	f := newFixture(t)
	id := opaqueID(t)
	p := invite.Packet{Version: 1, InviteID: id, Enc: make([]byte, 32), Ciphertext: make([]byte, 32)}
	input := map[string]any{"proof_key": public(f.reader), "packet": p, "expires_at": time.Now().Add(time.Hour).Unix(), "voucher": strings.Repeat("v", 32)}
	f.request(t, "POST", "/v1/invites/"+id, input, f.writer, 403)
	for range 2 {
		response := f.request(t, "POST", "/v1/invites/"+id, input, f.admin, 201)
		if bytes.Contains(response, []byte(strings.Repeat("v", 32))) {
			t.Fatal("voucher leaked")
		}
	}
	f.request(t, "GET", "/v1/invites/"+id, nil, f.writer, 200)
	p.InviteID = opaqueID(t)
	input["packet"] = p
	f.request(t, "POST", "/v1/invites/"+id, input, f.admin, 400)
	input["packet"] = invite.Packet{Version: 1, InviteID: id, Enc: make([]byte, 32), Ciphertext: make([]byte, 32)}
	input["scope"] = "coauthor"
	f.request(t, "POST", "/v1/invites/"+id, input, f.admin, 400)
}
