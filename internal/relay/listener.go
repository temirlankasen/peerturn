package relay

import (
	"net"
	"net/netip"
	"sync"
)

type boundedListener struct {
	net.Listener
	slots    chan struct{}
	done     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	bySource map[string]int
	policy   func(net.Addr) (string, int)
}

// Cap sockets before TLS handshakes: HTTP request quotas do not bound idle peers.
func limitListener(listener net.Listener, limit int) net.Listener {
	return limitListenerWithPolicy(listener, limit, func(address net.Addr) (string, int) { return sourcePrefix(address, 64), min(8, limit) })
}
func limitListenerWithPolicy(listener net.Listener, limit int, policy func(net.Addr) (string, int)) net.Listener {
	return &boundedListener{Listener: listener, slots: make(chan struct{}, limit), done: make(chan struct{}), bySource: make(map[string]int), policy: policy}
}
func (l *boundedListener) Accept() (net.Conn, error) {
	for {
		select {
		case <-l.done:
			return nil, net.ErrClosed
		case l.slots <- struct{}{}:
		}
		c, err := l.Listener.Accept()
		if err != nil {
			<-l.slots
			return nil, err
		}
		key, capSource := l.policy(c.RemoteAddr())
		l.mu.Lock()
		allowed := key != "" && l.bySource[key] < capSource
		if allowed {
			l.bySource[key]++
		}
		l.mu.Unlock()
		if !allowed {
			<-l.slots
			closeRejected(c)
			continue
		}
		return &boundedConn{Conn: c, release: func() {
			l.mu.Lock()
			l.bySource[key]--
			if l.bySource[key] == 0 {
				delete(l.bySource, key)
			}
			l.mu.Unlock()
			<-l.slots
		}}, nil
	}
}
func (l *boundedListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.Listener.Close()
}

type boundedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *boundedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

func closeRejected(c net.Conn) {
	if err := c.Close(); err != nil {
		return
	}
}

func sourcePrefix(address net.Addr, bits int) string {
	host, _, err := net.SplitHostPort(address.String())
	if err != nil {
		return ""
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" {
		return ""
	}
	ip = ip.Unmap()
	if ip.Is6() {
		return netip.PrefixFrom(ip, bits).Masked().String()
	}
	return ip.String()
}

func (s *Server) connectionPolicy(address net.Addr) (string, int) {
	key := sourcePrefix(address, s.cfg.Limits.IPv6PrefixBits)
	host, _, err := net.SplitHostPort(address.String())
	if err == nil {
		if ip, parseErr := netip.ParseAddr(host); parseErr == nil && s.trustedIP(ip) {
			return key, max(1, s.cfg.Limits.MaxConnections/2)
		}
	}
	return key, s.cfg.Limits.RequestsPerIP
}
