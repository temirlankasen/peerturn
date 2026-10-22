package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/temirlankasen/peerturn/internal/relay/config"
)

// Run owns both listeners and drains HTTP custody operations before closing SQLite.
func Run(ctx context.Context, cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	s, err := New(cfg)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := s.Close(context.Background()); closeErr != nil {
			return
		}
	}()
	var lc net.ListenConfig
	public, err := lc.Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return err
	}
	public = limitListenerWithPolicy(public, cfg.Limits.MaxConnections, s.connectionPolicy)
	defer func() {
		if closeErr := public.Close(); closeErr != nil {
			return
		}
	}()
	private, err := lc.Listen(ctx, "tcp", cfg.PrivateListen)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := private.Close(); closeErr != nil {
			return
		}
	}()
	if cfg.TLSCert != "" {
		cert, tlsErr := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if tlsErr != nil {
			return tlsErr
		}
		public = tls.NewListener(public, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}})
	}
	api, ops := s.httpServer(s.Handler()), s.httpServer(s.PrivateHandler())
	results := make(chan error, 2)
	go func() { results <- api.Serve(public) }()
	go func() { results <- ops.Serve(private) }()
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-results:
	}
	s.cancel()
	drain, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	apiErr := api.Shutdown(drain)
	opsErr := ops.Shutdown(drain)
	if apiErr != nil {
		apiErr = errors.Join(apiErr, api.Close())
	}
	if opsErr != nil {
		opsErr = errors.Join(opsErr, ops.Close())
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, apiErr, opsErr)
}

func (s *Server) httpServer(handler http.Handler) *http.Server {
	return &http.Server{Handler: handler, ReadHeaderTimeout: time.Duration(s.cfg.Limits.ReadHeaderSeconds) * time.Second,
		ReadTimeout: time.Duration(s.cfg.Limits.BodyReadSeconds) * time.Second, WriteTimeout: s.cfg.RequestTimeout(), IdleTimeout: time.Duration(s.cfg.Limits.IdleSeconds) * time.Second,
		MaxHeaderBytes: 16 * 1024, ErrorLog: log.New(io.Discard, "", 0)}
}

// PrivateHandler returns bounded aggregate diagnostics without identifiers or credentials.
func (s *Server) PrivateHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { reply(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := s.db.Ping(ctx); err != nil {
			failure(w, 503, "storage_unavailable")
			return
		}
		reply(w, 200, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		s.mu.Lock()
		streams := s.streamCount
		s.mu.Unlock()
		write(w, fmt.Appendf(nil, "# TYPE peerturn_relay_up gauge\npeerturn_relay_up 1\n# TYPE peerturn_relay_requests_total counter\npeerturn_relay_requests_total %d\n# TYPE peerturn_relay_auth_denied_total counter\npeerturn_relay_auth_denied_total %d\n# TYPE peerturn_relay_streams gauge\npeerturn_relay_streams %d\n# TYPE peerturn_relay_sweep_failures_total counter\npeerturn_relay_sweep_failures_total %d\n", s.requests.Load(), s.authDenied.Load(), streams, s.sweepFailures.Load()))
	})
	return mux
}
