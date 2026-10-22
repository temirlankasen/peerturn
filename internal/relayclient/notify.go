package relayclient

import (
	"context"
	"crypto/rand"
	"errors"
	"net/url"
	"time"

	"github.com/coder/websocket"
	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// Notify reconnects a signed notification-only WSS subscription with bounded jitter.
// Every connection reloads durable custody; notification high_seq never becomes ACK.
// Callback errors stop the subscription so failed local commits cannot be hidden.
func (c *Client) Notify(ctx context.Context, mailbox string, opts NotifyOptions) error {
	if !protocol.ValidToken(mailbox) || opts.Frontier == nil || opts.OnEvent == nil {
		return invalid()
	}
	if err := c.checkConsistency(ctx); err != nil {
		return err
	}
	backoff := 250 * time.Millisecond
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		frontier, err := opts.Frontier(ctx)
		if err != nil {
			return err
		}
		if frontier < 0 || frontier > protocol.MaxSafeInteger {
			return invalid()
		}
		stable, err := c.subscribe(ctx, mailbox, frontier, opts)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !retryable(err) {
			return err
		}
		if stable {
			backoff = 250 * time.Millisecond
		}
		if err = reconnectWait(ctx, backoff); err != nil {
			return err
		}
		backoff = min(5*time.Second, backoff*2)
	}
}

func (c *Client) subscribe(ctx context.Context, mailbox string, frontier int64, opts NotifyOptions) (stable bool, result error) {
	r, err := c.request(ctx, "GET", "/v1/events", url.Values{"mailbox": {mailbox}}, nil, true)
	if err != nil {
		return false, err
	}
	u := *r.URL
	u.Scheme = "wss"
	conn, response, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: r.Header, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		if response != nil && response.Body != nil {
			problem := responseError(response)
			if closeErr := response.Body.Close(); closeErr != nil {
				return false, problem
			}
			return false, problem
		}
		return false, &Error{Code: "transport_unavailable"}
	}
	defer closeStream(conn)
	connectedAt := time.Now()
	defer func() {
		// A single event followed by a close cannot drive a rapid reconnect loop.
		stable = time.Since(connectedAt) >= 30*time.Second
	}()
	conn.SetReadLimit(4096)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go keepAlive(streamCtx, conn)
	received := false
	for {
		kind, data, readErr := conn.Read(streamCtx)
		if readErr != nil {
			if websocket.CloseStatus(readErr) == websocket.StatusMessageTooBig || websocket.CloseStatus(readErr) == websocket.StatusProtocolError {
				return false, malformed()
			}
			return false, &Error{Code: "transport_unavailable"}
		}
		var event struct {
			Mailbox string `json:"mailbox"`
			HighSeq *int64 `json:"high_seq"`
		}
		if kind != websocket.MessageText || codec.Decode(data, 4096, &event) != nil || event.Mailbox != mailbox || event.HighSeq == nil || *event.HighSeq < 0 || *event.HighSeq > protocol.MaxSafeInteger {
			return false, malformed()
		}
		if received {
			frontier, err = opts.Frontier(ctx)
			if err != nil {
				return false, &callbackError{err}
			}
			if frontier < 0 || frontier > protocol.MaxSafeInteger {
				return false, invalid()
			}
		}
		received = true
		if err = opts.OnEvent(ctx, Event{Mailbox: event.Mailbox, HighSeq: *event.HighSeq, After: frontier}); err != nil {
			return false, &callbackError{err}
		}
	}
}

// Callback failures are never transport retries, even if the callback used a relay client.
type callbackError struct{ err error }

func (e *callbackError) Error() string { return e.err.Error() }
func (e *callbackError) Unwrap() error { return e.err }

func retryable(err error) bool {
	if _, ok := errors.AsType[*callbackError](err); ok {
		return false
	}
	problem, ok := errors.AsType[*Error](err)
	return ok && (problem.Code == "transport_unavailable" || problem.Status == 408 || problem.Status == 429 || problem.Status >= 500)
}

func reconnectWait(ctx context.Context, delay time.Duration) error {
	var random [1]byte
	if _, err := rand.Read(random[:]); err != nil {
		return &Error{Code: "reconnect_unavailable"}
	}
	jitter := delay * time.Duration(random[0]) / 512
	timer := time.NewTimer(delay/2 + jitter)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func keepAlive(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(check)
			cancel()
			if err != nil {
				closeStream(conn)
				return
			}
		}
	}
}

func closeStream(conn *websocket.Conn) {
	if err := conn.CloseNow(); err != nil {
		return
	}
}
