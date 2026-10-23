// Package relay provides a ciphertext mailbox, never an agent execution surface.
package relay

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
	"github.com/temirlankasen/peerturn/internal/relay/config"
	"github.com/temirlankasen/peerturn/internal/relay/store"
)

// Server owns the database and cancellation fence for all open notification streams.
type Server struct {
	cfg            config.Config
	db             *store.Store
	verifier       *auth.Verifier
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.Mutex
	mailChanges    map[string]*mailWatch
	streams        map[string]int
	mailStreams    map[string]int
	keyPolls       map[string]int
	mailPolls      map[string]int
	streamCount    int
	relayKey       string
	buckets        map[string]bucket
	rateMu         sync.Mutex
	bucketOrder    list.List
	ipActive       map[string]int
	ipExecuting    map[string]int
	now            func() time.Time
	readNonces     map[string]time.Time
	readNonceOrder []readNonce
	readNonceHead  int
	readNonceKeys  map[string]int
	anonymous      bucket
	slots          chan struct{}
	polls          chan struct{}
	closed         bool
	requests       atomic.Uint64
	authDenied     atomic.Uint64
	sweepFailures  atomic.Uint64
	maintenance    sync.WaitGroup
}

// New opens a single durable instance. The runtime validates listener/TLS settings.
func New(cfg config.Config) (*Server, error) {
	return newWithClock(cfg, time.Now)
}

func newWithClock(cfg config.Config, now func() time.Time) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	db, err := store.Open(filepath.Join(cfg.DataDir, "relay.sqlite"), store.Limits{
		GlobalBytes: cfg.Limits.TotalBytes, MailboxItems: cfg.Limits.MailboxItems,
		MailboxBytes: cfg.Limits.MailboxBytes, MetadataItems: cfg.Limits.MetadataItems,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	db.SetClock(now)
	s := &Server{cfg: cfg, db: db, ctx: ctx, cancel: cancel, now: now,
		mailChanges: make(map[string]*mailWatch),
		streams:     make(map[string]int), mailStreams: make(map[string]int), keyPolls: make(map[string]int), mailPolls: make(map[string]int),
		buckets: make(map[string]bucket), ipActive: make(map[string]int), ipExecuting: make(map[string]int),
		readNonces: make(map[string]time.Time), readNonceKeys: make(map[string]int),
		slots: make(chan struct{}, 64), polls: make(chan struct{}, 32)}
	s.verifier, err = auth.NewVerifier(auth.Options{Now: now, PublicAuthority: cfg.PublicAuthority,
		TrustedProxies: cfg.TrustedProxyPrefixes(), Authorize: s.authorizeRequest, Replay: s.replay})
	if err == nil {
		err = s.bootstrap()
	}
	if err != nil {
		cancel()
		return nil, errors.Join(err, db.Close())
	}
	s.startMaintenance()
	return s, nil
}

// Close cancels streams before releasing storage, so shutdown cannot keep stale authority.
func (s *Server) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.maintenance.Wait()
	return s.db.Close()
}

// Handler excludes diagnostics so public ingress cannot expose operational metadata.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/peerturn", s.discovery)
	mux.HandleFunc("GET /i/{invite}", s.invitePage)
	mux.HandleFunc("POST /v1/mailboxes", s.protected(s.createMailbox))
	mux.HandleFunc("DELETE /v1/mailboxes/{mailbox}", s.protected(s.deleteMailbox))
	mux.HandleFunc("GET /v1/invites/{invite}", s.protected(s.getInvite))
	mux.HandleFunc("POST /v1/invites/{invite}", s.protected(s.createInvite))
	mux.HandleFunc("POST /v1/invites/{invite}/claims", s.protected(s.claimInvite))
	mux.HandleFunc("PUT /v1/mailboxes/{mailbox}/permits/{permit}", s.protected(s.putPermit))
	mux.HandleFunc("DELETE /v1/mailboxes/{mailbox}/permits/{permit}", s.protected(s.revokePermit))
	mux.HandleFunc("POST /v1/mailboxes/{mailbox}/messages", s.protected(s.postMessage))
	mux.HandleFunc("GET /v1/mailboxes/{mailbox}/messages", s.protected(s.getMessages))
	mux.HandleFunc("POST /v1/mailboxes/{mailbox}/ack", s.protected(s.ack))
	mux.HandleFunc("POST /v1/streams/tickets", s.protected(s.ticket))
	mux.HandleFunc("GET /v1/events", s.events)
	return s.admit(mux)
}

type endpoint func(http.ResponseWriter, *http.Request, string, []byte)

func (s *Server) protected(next endpoint) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout())
		defer cancel()
		r = r.WithContext(ctx)
		body, key, ok := s.authenticate(w, r)
		if ok {
			next(w, r, key, body)
		}
	}
}

func decode(w http.ResponseWriter, data []byte, v any) bool {
	if err := codec.Decode(data, protocol.MaxPacket, v); err != nil {
		failure(w, 400, "invalid_schema")
		return false
	}
	return true
}

func reply(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		failure(w, 503, "unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	write(w, data)
}
func write(w http.ResponseWriter, data []byte) {
	if _, err := w.Write(data); err != nil {
		return
	}
}
func failure(w http.ResponseWriter, status int, code string) {
	if status == 429 {
		w.Header().Set("Connection", "close")
	}
	if status == 429 || status == 503 {
		w.Header().Set("Retry-After", "1")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	write(w, []byte(`{"error":"`+code+`"}`))
}
func storageError(w http.ResponseWriter, err error) {
	if e, ok := errors.AsType[*store.Error](err); ok {
		failure(w, e.Status, e.Code)
		return
	}
	failure(w, 503, "storage_unavailable")
}
