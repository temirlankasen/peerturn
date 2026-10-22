package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/temirlankasen/peerturn/internal/relay/auth"
	"github.com/temirlankasen/peerturn/internal/relay/store"
)

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) ([]byte, string, bool) {
	a, ok := r.Context().Value(admissionKey{}).(*admission)
	if !ok {
		failure(w, 503, "unavailable")
		return nil, "", false
	}
	key, err := s.verifier.Verify(r.Context(), r, a.body)
	if err != nil {
		s.authDenied.Add(1)
		switch {
		case errors.Is(err, auth.ErrForbidden):
			failure(w, 403, "forbidden")
		case errors.Is(err, auth.ErrRateLimited):
			failure(w, 429, "rate_limit")
		case errors.Is(err, auth.ErrUnavailable):
			storageError(w, err)
		default:
			failure(w, 401, "unauthorized")
		}
		return nil, "", false
	}
	return a.body, key, true
}

// A self-certifying signature proves possession, not tenancy. Bind it to this
// endpoint before it can spend durable nonce space or enter the owner lane.
func (s *Server) authorizeRequest(ctx context.Context, r *http.Request, key string) error {
	a, ok := ctx.Value(admissionKey{}).(*admission)
	if !ok {
		return auth.ErrUnavailable
	}
	if !s.allow("key:"+key, s.now()) {
		return auth.ErrRateLimited
	}
	mailbox := r.PathValue("mailbox")
	if mailbox == "" && r.URL.Path == "/v1/events" {
		mailbox = r.URL.Query().Get("mailbox")
	}
	if r.URL.Path == "/v1/streams/tickets" {
		var input subscription
		if err := json.Unmarshal(a.body, &input); err != nil {
			return auth.ErrForbidden
		}
		mailbox = input.Mailbox
	}
	if mailbox != "" {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/messages") {
			known, err := s.db.KnownMailboxKey(ctx, mailbox, key, s.now())
			if err != nil {
				return err
			}
			if !known {
				return auth.ErrForbidden
			}
		} else {
			if err := s.db.CheckReader(ctx, mailbox, key); err != nil {
				return bindingError(err)
			}
		}
		a.read = r.Method == "GET"
		// Growing ACL/ticket rows cannot spend the reserve needed to free space.
		a.control = r.Method == "DELETE" || (r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/ack"))
		return nil
	}
	if r.URL.Path == "/v1/mailboxes" && s.admin(key) {
		return nil
	}
	if r.Method == "POST" && r.PathValue("invite") != "" && !strings.HasSuffix(r.URL.Path, "/claims") {
		if !s.admin(key) {
			return auth.ErrForbidden
		}
		return nil
	}
	if r.URL.Path == "/v1/mailboxes" {
		var input struct {
			ReadKey string `json:"read_key"`
			Voucher string `json:"voucher"`
		}
		if err := json.Unmarshal(a.body, &input); err != nil || input.ReadKey != key || input.Voucher == "" {
			return auth.ErrForbidden
		}
	} else if !strings.HasPrefix(r.URL.Path, "/v1/invites/") {
		return auth.ErrForbidden
	}
	if !s.allowAnonymous(s.now()) {
		return auth.ErrRateLimited
	}
	a.anonymous = true
	a.read = r.Method == "GET"
	return nil
}

func bindingError(err error) error {
	if e, ok := errors.AsType[*store.Error](err); ok && e.Status == 403 {
		return auth.ErrForbidden
	}
	return err
}

func (s *Server) allowAnonymous(now time.Time) bool {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	b := s.anonymous
	if b.at.IsZero() {
		b = bucket{tokens: 16, at: now}
	}
	b.tokens = min(16, b.tokens+max(0, now.Sub(b.at).Seconds())*2)
	b.at = now
	ok := b.tokens >= 1
	if ok {
		b.tokens--
	}
	s.anonymous = b
	return ok
}

func (s *Server) replay(ctx context.Context, key, nonce string, expires int64) error {
	a, ok := ctx.Value(admissionKey{}).(*admission)
	if !ok {
		return auth.ErrUnavailable
	}
	until, now := time.Unix(expires, 0), s.now()
	if a.read {
		return s.useReadNonce(key, nonce, until, now)
	}
	var err error
	switch {
	case a.control:
		err = s.db.UseControlNonce(ctx, key, nonce, until, now)
	case a.anonymous:
		err = s.db.UseAnonymousNonce(ctx, key, nonce, until, now)
	default:
		err = s.db.UseNonce(ctx, key, nonce, until, now)
	}
	if problem, ok := errors.AsType[*store.Error](err); ok && problem.Code == "replay" {
		return auth.ErrReplay
	}
	return err
}

type readNonce struct {
	id, key string
	until   time.Time
}

// Replaying a read never repeats a custody effect. Its separate, volatile replay
// cache avoids syncing an empty poll while durable mutations retain FULL sync.
func (s *Server) useReadNonce(key, nonce string, until, now time.Time) error {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	for s.readNonceHead < len(s.readNonceOrder) && !s.readNonceOrder[s.readNonceHead].until.After(now) {
		old := s.readNonceOrder[s.readNonceHead]
		delete(s.readNonces, old.id)
		s.readNonceKeys[old.key]--
		if s.readNonceKeys[old.key] == 0 {
			delete(s.readNonceKeys, old.key)
		}
		s.readNonceHead++
	}
	if s.readNonceHead > 1024 {
		s.readNonceOrder = append([]readNonce(nil), s.readNonceOrder[s.readNonceHead:]...)
		s.readNonceHead = 0
	}
	id := key + ":" + nonce
	if _, exists := s.readNonces[id]; exists {
		return auth.ErrReplay
	}
	if len(s.readNonces) >= max(256, s.cfg.Limits.MetadataItems) || s.readNonceKeys[key] >= 256 {
		return &store.Error{Code: "quota", Status: 429}
	}
	s.readNonces[id] = until
	s.readNonceKeys[key]++
	s.readNonceOrder = append(s.readNonceOrder, readNonce{id: id, key: key, until: until})
	return nil
}
