package relay

import (
	"net"
	"testing"
)

type sourceConn struct {
	net.Conn
	remote net.Addr
}

func (c *sourceConn) RemoteAddr() net.Addr { return c.remote }

type scriptedListener struct {
	net.Listener
	connections []net.Conn
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	if len(l.connections) == 0 {
		return nil, net.ErrClosed
	}
	c := l.connections[0]
	l.connections = l.connections[1:]
	return c, nil
}

func TestOneSourceCannotFillListener(t *testing.T) {
	raw := &scriptedListener{}
	for i := range 10 {
		server, client := net.Pipe()
		t.Cleanup(func() {
			if err := server.Close(); err != nil {
				t.Error(err)
			}
			if err := client.Close(); err != nil {
				t.Error(err)
			}
		})
		ip := "198.51.100.10"
		if i == 9 {
			ip = "198.51.100.11"
		}
		raw.connections = append(raw.connections, &sourceConn{Conn: server, remote: &net.TCPAddr{IP: net.ParseIP(ip), Port: 1234}})
	}
	listener := limitListener(raw, 16)
	for range 8 {
		c, err := listener.Accept()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if closeErr := c.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		})
	}
	c, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := c.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if c.RemoteAddr().String() != "198.51.100.11:1234" {
		t.Fatalf("one source consumed ninth socket: %s", c.RemoteAddr())
	}
}

func TestSocketSourcePolicyAggregatesIPv6AndBoundsProxy(t *testing.T) {
	f := newFixture(t)
	var first string
	for _, raw := range []string{"2001:db8:1::1", "2001:db8:1::abcd"} {
		source, limit := f.s.connectionPolicy(&net.TCPAddr{IP: net.ParseIP(raw), Port: 1234})
		if limit != f.s.cfg.Limits.RequestsPerIP {
			t.Fatalf("source limit=%d", limit)
		}
		if first == "" {
			first = source
		} else if source != first {
			t.Fatalf("same /64 split into %q and %q", first, source)
		}
	}
	_, limit := f.s.connectionPolicy(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234})
	if limit != f.s.cfg.Limits.MaxConnections/2 {
		t.Fatalf("proxy socket limit=%d", limit)
	}
}
