package relay

import (
	"container/list"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

type bucket struct {
	tokens  float64
	at      time.Time
	element *list.Element
}

type leaseKey struct{}
type requestLease struct {
	once    sync.Once
	release func()
}

func (l *requestLease) close() { l.once.Do(l.release) }
func releaseRequestSlot(r *http.Request) {
	if lease, ok := r.Context().Value(leaseKey{}).(*requestLease); ok {
		lease.close()
	}
}

// LRU bounds memory without allowing a full table to exclude all new clients.
func (s *Server) allow(id string, now time.Time) bool {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	b, exists := s.buckets[id]
	if !exists {
		if len(s.buckets) == 0 {
			s.bucketOrder.Init()
		}
		if len(s.buckets) >= s.cfg.Limits.MetadataItems {
			oldest := s.bucketOrder.Back()
			oldID, ok := oldest.Value.(string)
			if !ok {
				return false
			}
			delete(s.buckets, oldID)
			s.bucketOrder.Remove(oldest)
		}
		b = bucket{tokens: float64(s.cfg.Limits.RateBurst), at: now, element: s.bucketOrder.PushFront(id)}
	}
	b.tokens = min(float64(s.cfg.Limits.RateBurst), b.tokens+max(0, now.Sub(b.at).Seconds())*float64(s.cfg.Limits.RatePerMinute)/60)
	b.at = now
	ok := b.tokens >= 1
	if ok {
		b.tokens--
	}
	s.bucketOrder.MoveToFront(b.element)
	s.buckets[id] = b
	return ok
}

func (s *Server) trustedIP(ip netip.Addr) bool {
	for _, prefix := range s.cfg.TrustedProxyPrefixes() {
		if prefix.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}

// Only an allowlisted immediate proxy can supply a chain. Walking right to left
// stops at the first untrusted hop, so client-prepended addresses are ignored.
func (s *Server) clientIP(r *http.Request) (string, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "", false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" {
		return "", false
	}
	ip = ip.Unmap()
	if s.trustedIP(ip) && len(r.Header.Values(s.cfg.ClientIPHeader)) > 0 {
		values := r.Header.Values(s.cfg.ClientIPHeader)
		if len(values) != 1 || len(values[0]) > 4096 {
			return "", false
		}
		chain := strings.Split(values[0], ",")
		if len(chain) > 32 || (s.cfg.ClientIPHeader == "X-Real-IP" && len(chain) != 1) {
			return "", false
		}
		for _, hop := range slices.Backward(chain) {
			next, parseErr := netip.ParseAddr(strings.TrimSpace(hop))
			if parseErr != nil || next.Zone() != "" {
				return "", false
			}
			ip = next.Unmap()
			if !s.trustedIP(ip) {
				break
			}
		}
	}
	if ip.Is6() {
		return netip.PrefixFrom(ip, s.cfg.Limits.IPv6PrefixBits).Masked().String(), true
	}
	return ip.String(), true
}

func (s *Server) reserveIP(ip string) bool {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	if s.ipActive[ip] >= s.cfg.Limits.RequestsPerIP {
		return false
	}
	s.ipActive[ip]++
	return true
}
func (s *Server) releaseIP(ip string) {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	s.ipActive[ip]--
	if s.ipActive[ip] == 0 {
		delete(s.ipActive, ip)
	}
}
