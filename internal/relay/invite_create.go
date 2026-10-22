package relay

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/relay/store"
)

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request, key string, body []byte) {
	if !s.admin(key) {
		failure(w, 403, "forbidden")
		return
	}
	var input struct {
		ProofKey  string          `json:"proof_key"`
		Packet    json.RawMessage `json:"packet"`
		ExpiresAt int64           `json:"expires_at"`
		Voucher   string          `json:"voucher,omitempty"`
	}
	if !decode(w, body, &input) {
		return
	}
	packet, err := invite.DecodePacket(input.Packet)
	if err != nil {
		protocolError(w, err)
		return
	}
	if packet.InviteID != r.PathValue("invite") || !validKey(input.ProofKey) || input.ExpiresAt <= s.now().Unix() || input.ExpiresAt > (1<<63-1)/int64(time.Second) || len(input.Voucher) < 32 || len(input.Voucher) > 256 {
		failure(w, 400, "invalid_schema")
		return
	}
	canonical, err := codec.Canonical(packet)
	if err != nil {
		protocolError(w, err)
		return
	}
	if err = s.db.PutInvite(r.Context(), store.Invite{ID: packet.InviteID, ProofKey: input.ProofKey, Packet: canonical, ExpiresAt: time.Unix(input.ExpiresAt, 0), Voucher: input.Voucher}); err != nil {
		storageError(w, err)
		return
	}
	reply(w, 201, map[string]string{"id": packet.InviteID})
}
