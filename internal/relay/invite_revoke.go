package relay

import "net/http"

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request, key string, _ []byte) {
	if err := s.db.RevokeExistingInvite(r.Context(), r.PathValue("invite"), key, s.admin(key)); err != nil {
		storageError(w, err)
		return
	}
	reply(w, 200, map[string]bool{"revoked": true})
}

func (s *Server) blockInviter(w http.ResponseWriter, r *http.Request, key string, _ []byte) {
	if !s.admin(key) {
		failure(w, 403, "forbidden")
		return
	}
	inviter := r.PathValue("inviter")
	if !validKey(inviter) {
		failure(w, 400, "invalid_schema")
		return
	}
	if err := s.db.BlockInviter(r.Context(), inviter); err != nil {
		storageError(w, err)
		return
	}
	reply(w, 200, map[string]bool{"revoked": true})
}
