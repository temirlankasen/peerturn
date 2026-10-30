package relay

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/relay/config"
	"github.com/temirlankasen/peerturn/internal/relay/store"
)

type invitePublicationReceipt struct {
	ID      string `json:"id"`
	Mailbox string `json:"mailbox,omitempty"`
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request, key string, body []byte) {
	selfService := s.cfg.AdmissionMode == config.AdmissionSelfService
	var input struct {
		ProofKey          string          `json:"proof_key"`
		Packet            json.RawMessage `json:"packet"`
		ExpiresAt         int64           `json:"expires_at"`
		Voucher           string          `json:"voucher,omitempty"`
		RequireOwnMailbox bool            `json:"require_own_mailbox,omitempty"`
	}
	if !decode(w, body, &input) {
		return
	}
	packet, err := invite.DecodePacket(input.Packet)
	if err != nil {
		protocolError(w, err)
		return
	}
	if packet.InviteID != r.PathValue("invite") || !validKey(input.ProofKey) || input.ExpiresAt <= 0 || input.ExpiresAt > (1<<63-1)/int64(time.Second) || len(input.Voucher) < 32 || len(input.Voucher) > 256 {
		failure(w, 400, "invalid_schema")
		return
	}
	canonical, err := codec.Canonical(packet)
	if err != nil {
		protocolError(w, err)
		return
	}
	descriptor := store.Invite{ID: packet.InviteID, ProofKey: input.ProofKey, Packet: canonical, ExpiresAt: time.Unix(input.ExpiresAt, 0), Voucher: input.Voucher}
	var mailbox store.Mailbox
	if selfService {
		// The verified routing signer owns admission; the invitation proof key does not.
		mailbox, err = s.db.CreateOwnInvite(r.Context(), key, descriptor, store.SelfServicePolicy{
			MaxActiveInvites:   s.cfg.SelfService.ActiveInvites,
			MaxInvitesPer24h:   s.cfg.SelfService.DailyInvites,
			MaxInvitesLifetime: s.cfg.SelfService.LifetimeInvites,
			MaxInviters:        s.cfg.SelfService.Inviters,
		})
	} else {
		mailbox, err = s.db.PutClosedInvite(r.Context(), key, descriptor, s.admin(key) && !input.RequireOwnMailbox)
	}
	if err != nil {
		storageError(w, err)
		return
	}
	reply(w, 201, invitePublicationReceipt{ID: packet.InviteID, Mailbox: mailbox.ID})
}
