package relay

import (
	"encoding/base64"
	"net/http"
	"slices"
	"time"

	ptcrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relay/store"
)

func validKey(key string) bool {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(key)
	return err == nil && base64.RawURLEncoding.EncodeToString(raw) == key && ptcrypto.ValidSigningKey(raw)
}
func (s *Server) admin(key string) bool {
	return slices.Contains(s.cfg.AdminKeys, key)
}

func (s *Server) createMailbox(w http.ResponseWriter, r *http.Request, key string, body []byte) {
	var input struct {
		ID      string `json:"id"`
		ReadKey string `json:"read_key"`
		Voucher string `json:"voucher,omitempty"`
	}
	if !decode(w, body, &input) {
		return
	}
	if !protocol.ValidToken(input.ID) || !validKey(input.ReadKey) || len(input.Voucher) > 256 {
		failure(w, 400, "invalid_schema")
		return
	}
	if !s.admin(key) && (input.Voucher == "" || key != input.ReadKey) {
		failure(w, 403, "enrollment_denied")
		return
	}
	voucher := input.Voucher
	if s.admin(key) {
		voucher = ""
	}
	if err := s.db.CreateMailbox(r.Context(), store.Mailbox{ID: input.ID, ReadKey: input.ReadKey}, voucher); err != nil {
		storageError(w, err)
		return
	}
	reply(w, 201, map[string]string{"mailbox": input.ID})
}

func (s *Server) putPermit(w http.ResponseWriter, r *http.Request, key string, body []byte) {
	var input struct {
		WriterKey string `json:"writer_key"`
		Epoch     int64  `json:"epoch"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if !decode(w, body, &input) {
		return
	}
	// Reserve the final interoperable epoch so every accepted live permit remains revocable.
	if !protocol.ValidToken(r.PathValue("permit")) || !validKey(input.WriterKey) || input.Epoch <= 0 || input.Epoch >= protocol.MaxSafeInteger || input.ExpiresAt <= s.now().Unix() || input.ExpiresAt > (1<<63-1)/int64(time.Second) {
		failure(w, 400, "invalid_schema")
		return
	}
	p := store.Permit{ID: r.PathValue("permit"), WriterKey: input.WriterKey, Epoch: input.Epoch, ExpiresAt: time.Unix(input.ExpiresAt, 0)}
	if err := s.db.PutPermit(r.Context(), r.PathValue("mailbox"), key, p); err != nil {
		storageError(w, err)
		return
	}
	s.signal(r.PathValue("mailbox"))
	reply(w, 200, map[string]int64{"epoch": input.Epoch})
}

func (s *Server) revokePermit(w http.ResponseWriter, r *http.Request, key string, body []byte) {
	var input struct {
		Epoch int64 `json:"epoch"`
	}
	if !decode(w, body, &input) {
		return
	}
	if input.Epoch <= 0 || input.Epoch > protocol.MaxSafeInteger {
		failure(w, 400, "invalid_schema")
		return
	}
	if err := s.db.RevokePermit(r.Context(), r.PathValue("mailbox"), key, r.PathValue("permit"), input.Epoch); err != nil {
		storageError(w, err)
		return
	}
	s.signal(r.PathValue("mailbox"))
	reply(w, 200, map[string]int64{"epoch": input.Epoch})
}

func (s *Server) deleteMailbox(w http.ResponseWriter, r *http.Request, key string, _ []byte) {
	if err := s.db.DeleteMailbox(r.Context(), r.PathValue("mailbox"), key); err != nil {
		storageError(w, err)
		return
	}
	s.signal(r.PathValue("mailbox"))
	reply(w, 200, map[string]bool{"deleted": true})
}
