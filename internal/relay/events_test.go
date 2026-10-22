package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
)

func (f *fixture) dial(t *testing.T, path string, signer ed25519.PrivateKey) (*websocket.Conn, int, error) {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), "GET", f.h.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if signer != nil {
		nonce := make([]byte, 16)
		if _, err = rand.Read(nonce); err != nil {
			t.Fatal(err)
		}
		now := f.s.now()
		if err = auth.Sign(r, nil, signer, now, now.Add(30*time.Second), base64.RawURLEncoding.EncodeToString(nonce)); err != nil {
			t.Fatal(err)
		}
	}
	conn, response, dialErr := websocket.Dial(t.Context(), strings.Replace(f.h.URL, "https:", "wss:", 1)+path, &websocket.DialOptions{HTTPClient: f.h.Client(), HTTPHeader: r.Header})
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	if response != nil && response.Body != nil {
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}
	return conn, status, dialErr
}

func closeConnection(t *testing.T, c *websocket.Conn) {
	t.Helper()
	if err := c.CloseNow(); err != nil {
		t.Error(err)
	}
}

func TestWriterNeverGetsNotifyStream(t *testing.T) {
	for _, change := range []string{"revoke", "replace"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t)
			f.enroll(t)
			conn, resp, err := f.dial(t, "/v1/events?mailbox=box&permit=write", f.writer)
			if conn != nil {
				closeConnection(t, conn)
			}
			if err == nil || resp != 403 {
				t.Fatalf("writer stream status=%d err=%v", resp, err)
			}
			if change == "revoke" {
				f.request(t, "DELETE", "/v1/mailboxes/box/permits/write", map[string]int{"epoch": 2}, f.reader, 200)
			} else {
				f.request(t, "PUT", "/v1/mailboxes/box/permits/write", map[string]any{"writer_key": public(f.writer), "epoch": 2, "expires_at": time.Now().Add(time.Hour).Unix()}, f.reader, 200)
			}
			conn, resp, err = f.dial(t, "/v1/events?mailbox=box&permit=write", f.writer)
			if conn != nil {
				closeConnection(t, conn)
			}
			if err == nil || resp != 403 {
				t.Fatalf("post-change stream status=%d err=%v", resp, err)
			}
		})
	}
}

func TestNotifyTicketOneUseAndNoContentAuthority(t *testing.T) {
	f := newFixture(t)
	f.enroll(t)
	data := f.request(t, "POST", "/v1/streams/tickets", map[string]string{"mailbox": "box"}, f.reader, 201)
	var ticket struct {
		Ticket    string `json:"ticket"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &ticket); err != nil {
		t.Fatal(err)
	}
	if ticket.ExpiresAt > time.Now().Add(time.Minute).Unix() {
		t.Fatal("ticket exceeds 60sec")
	}
	conn, _, err := f.dial(t, "/v1/events?ticket="+ticket.Ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeConnection(t, conn)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var event map[string]any
	if err = wsjson.Read(ctx, conn, &event); err != nil {
		t.Fatal(err)
	}
	other, resp, err := f.dial(t, "/v1/events?ticket="+ticket.Ticket, nil)
	if other != nil {
		closeConnection(t, other)
	}
	if err == nil || resp != 403 {
		t.Fatalf("reused ticket: resp=%v err=%v", resp, err)
	}
	f.request(t, "GET", "/v1/mailboxes/box/messages?ticket="+ticket.Ticket, nil, nil, 401)
	f.request(t, "POST", "/v1/streams/tickets", map[string]string{"mailbox": "box"}, f.writer, 403)
}
