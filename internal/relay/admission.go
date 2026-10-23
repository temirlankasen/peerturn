package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type admissionKey struct{}
type admission struct {
	body                     []byte
	control, anonymous, read bool
}

func (s *Server) admit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		ip, ok := s.clientIP(r)
		if !ok {
			failure(w, 400, "invalid_client_address")
			return
		}
		if !s.allow("ip:"+ip, s.now()) || !s.reserveIP(ip) {
			failure(w, 429, "rate_limit")
			return
		}
		ingress := &requestLease{release: func() { s.releaseIP(ip) }}
		defer ingress.close()
		// Control envelopes are small even when data messages allow larger bodies.
		bound := min(s.cfg.Limits.BodyBytes, 4096)
		if strings.HasSuffix(r.URL.Path, "/messages") || strings.HasSuffix(r.URL.Path, "/claims") || r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/v1/invites/") {
			bound = s.cfg.Limits.BodyBytes
		}
		headers := len(r.RequestURI) + len(r.Host)
		for k, vs := range r.Header {
			headers += len(k)
			for _, v := range vs {
				headers += len(v)
			}
		}
		if headers > 16<<10 {
			failure(w, 431, "headers_too_large")
			return
		}
		if r.ContentLength > bound {
			failure(w, 413, "too_large")
			return
		}
		controller := http.NewResponseController(w)
		// Wall time keeps stalled network reads bounded even with a frozen auth clock.
		if err := controller.SetReadDeadline(time.Now().Add(time.Duration(s.cfg.Limits.BodyReadSeconds) * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			failure(w, 503, "unavailable")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, bound))
		if err != nil {
			if _, large := errors.AsType[*http.MaxBytesError](err); large {
				failure(w, 413, "too_large")
			} else {
				failure(w, 400, "invalid_body")
			}
			return
		}
		if err = controller.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			failure(w, 503, "unavailable")
			return
		}
		// Socket/body allowance for shared NATs must not let one source monopolize execution.
		if !s.reserveExecution(ip) {
			failure(w, 429, "rate_limit")
			return
		}
		// Slow bodies never own a global execution slot.
		select {
		case s.slots <- struct{}{}:
		default:
			s.releaseExecution(ip)
			failure(w, 503, "backpressure")
			return
		}
		lease := &requestLease{release: func() { <-s.slots; s.releaseExecution(ip); ingress.close() }}
		defer lease.close()
		ctx := context.WithValue(r.Context(), leaseKey{}, lease)
		ctx = context.WithValue(ctx, admissionKey{}, &admission{body: body})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
