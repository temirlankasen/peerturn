package relay

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relay/store"
)

var errQuery = errors.New("invalid query")

func query(r *http.Request, allowed ...string) (url.Values, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, errQuery
	}
	for key, value := range values {
		if !slices.Contains(allowed, key) || len(value) != 1 || value[0] == "" {
			return nil, errQuery
		}
	}
	return values, nil
}

type subscription struct {
	Mailbox string `json:"mailbox"`
	Permit  string `json:"permit,omitempty"`
}

func (s *Server) subscription(r *http.Request, key string, input subscription) (store.Ticket, error) {
	t := store.Ticket{MailboxID: input.Mailbox, ReaderKey: key}
	if !protocol.ValidToken(input.Mailbox) {
		return t, &store.Error{Status: 400, Code: "invalid_mailbox"}
	}
	if input.Permit != "" {
		return t, &store.Error{Status: 403, Code: "forbidden"}
	}
	return t, s.db.CheckStream(r.Context(), t, s.now())
}

func (s *Server) ticket(w http.ResponseWriter, r *http.Request, key string, body []byte) {
	var input subscription
	if !decode(w, body, &input) {
		return
	}
	t, err := s.subscription(r, key, input)
	if err != nil {
		storageError(w, err)
		return
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		failure(w, 503, "unavailable")
		return
	}
	t.ID = base64.RawURLEncoding.EncodeToString(secret)
	t.ExpiresAt = s.now().Add(60 * time.Second)
	if err = s.db.PutTicket(r.Context(), t, s.now()); err != nil {
		storageError(w, err)
		return
	}
	reply(w, 201, map[string]any{"ticket": t.ID, "expires_at": t.ExpiresAt.Unix()})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	streamContext := r.Context()
	admissionContext, cancelAdmission := context.WithTimeout(streamContext, s.cfg.RequestTimeout())
	defer cancelAdmission()
	r = r.WithContext(admissionContext)
	q, err := query(r, "mailbox", "permit", "ticket")
	if err != nil {
		failure(w, 400, "invalid_query")
		return
	}
	var t store.Ticket
	var key string
	if token := q.Get("ticket"); token != "" {
		if err = s.verifier.ValidateTransport(r); err != nil {
			failure(w, 401, "insecure_transport")
			return
		}
		if len(q) != 1 || len(token) != 43 {
			failure(w, 400, "invalid_ticket")
			return
		}
		t, err = s.db.PeekTicket(r.Context(), token, s.now())
		key = t.ReaderKey
		if err != nil {
			storageError(w, err)
			return
		}
	} else {
		_, signer, ok := s.authenticate(w, r)
		if !ok {
			return
		}
		key = signer
		t, err = s.subscription(r, key, subscription{Mailbox: q.Get("mailbox"), Permit: q.Get("permit")})
		if err != nil {
			storageError(w, err)
			return
		}
	}
	if !s.reserveStream(key, t.MailboxID) {
		failure(w, 429, "stream_limit")
		return
	}
	defer s.releaseStream(key, t.MailboxID)
	if token := q.Get("ticket"); token != "" {
		if _, err = s.db.ConsumeTicket(r.Context(), token, s.now()); err != nil {
			storageError(w, err)
			return
		}
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	releaseRequestSlot(r)
	defer func() {
		if err := conn.CloseNow(); err != nil {
			return
		}
	}()
	conn.SetReadLimit(1)
	ctx, cancel := context.WithCancel(streamContext)
	defer cancel()
	// CloseRead rejects incoming data: this lane cannot become a second message API.
	ctx = conn.CloseRead(ctx)
	s.notify(ctx, conn, t)
}

func (s *Server) reserveStream(key, mailbox string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.streamCount >= s.cfg.Limits.MaxStreams || s.streams[key] >= s.cfg.Limits.StreamsPerKey || s.mailStreams[mailbox] >= s.cfg.Limits.StreamsPerMailbox {
		return false
	}
	s.streamCount++
	s.streams[key]++
	s.mailStreams[mailbox]++
	return true
}
func (s *Server) releaseStream(key, mailbox string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.streamCount--
	s.streams[key]--
	s.mailStreams[mailbox]--
	if s.mailStreams[mailbox] == 0 {
		delete(s.mailStreams, mailbox)
	}
	if s.streams[key] == 0 {
		delete(s.streams, key)
	}
}

func (s *Server) notify(ctx context.Context, conn *websocket.Conn, t store.Ticket) {
	watch, unwatch := s.watchMailbox(t.MailboxID)
	defer unwatch()
	last := int64(-1)
	for {
		changed := s.changes(watch)
		check, cancel := context.WithTimeout(ctx, time.Second)
		err := s.db.CheckStream(check, t, s.now())
		var high int64
		if err == nil {
			high, err = s.db.HighSeq(check, t.MailboxID)
		}
		cancel()
		if err != nil {
			return
		}
		if high != last {
			writeCtx, stop := context.WithTimeout(ctx, time.Second)
			err = wsjson.Write(writeCtx, conn, struct {
				Mailbox string `json:"mailbox"`
				HighSeq int64  `json:"high_seq"`
			}{t.MailboxID, high})
			stop()
			if err != nil {
				return
			}
			last = high
		}
		select {
		case <-ctx.Done():
			return
		case <-s.ctx.Done():
			return
		case <-changed:
		}
	}
}
