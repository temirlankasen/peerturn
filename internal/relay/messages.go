package relay

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relay/store"
)

func protocolError(w http.ResponseWriter, err error) {
	switch {
	case fault.IsCode(err, fault.UnsupportedVersion):
		failure(w, 426, "unsupported_version")
	case fault.IsCode(err, fault.TooLarge):
		failure(w, 413, "too_large")
	default:
		failure(w, 400, "invalid_schema")
	}
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request, key string, body []byte) {
	packet, err := protocol.DecodePacket(body)
	if err != nil {
		protocolError(w, err)
		return
	}
	h := packet.Header
	if h.DstMailbox != r.PathValue("mailbox") {
		failure(w, 400, "destination_mismatch")
		return
	}
	if len(packet.Ciphertext) > s.cfg.Limits.CiphertextBytes {
		failure(w, 413, "too_large")
		return
	}
	now := s.now()
	if h.ExpiresAt > now.Add(7*24*time.Hour).Unix() {
		failure(w, 400, "invalid_expiry")
		return
	}
	p, err := s.db.GetPermit(r.Context(), h.DstMailbox, h.RoutePermitID)
	if err != nil {
		storageError(w, err)
		return
	}
	canonical, err := codec.Canonical(packet)
	if err != nil {
		protocolError(w, err)
		return
	}
	receipt, err := s.db.PutMessage(r.Context(), store.Message{MailboxID: h.DstMailbox, PermitID: h.RoutePermitID, WriterKey: key, PermitEpoch: p.Epoch, ID: h.MessageID, Packet: canonical, ExpiresAt: time.Unix(h.ExpiresAt, 0)}, now)
	if err != nil {
		storageError(w, err)
		return
	}
	s.signal(h.DstMailbox)
	reply(w, 200, map[string]any{"stage": "relay_stored", "mailbox": h.DstMailbox, "message_id": h.MessageID, "seq": receipt.Seq})
}

type fetched struct {
	Seq       int64           `json:"seq"`
	Packet    json.RawMessage `json:"packet,omitempty"`
	Tombstone bool            `json:"tombstone,omitempty"`
}

func (s *Server) getMessages(w http.ResponseWriter, r *http.Request, key string, _ []byte) {
	q, err := query(r, "after", "limit", "wait")
	if err != nil {
		failure(w, 400, "invalid_query")
		return
	}
	after, err := number(q.Get("after"), 0, 0, protocol.MaxSafeInteger)
	if err != nil {
		failure(w, 400, "invalid_cursor")
		return
	}
	limit, err := number(q.Get("limit"), 100, 1, int64(s.cfg.Limits.BatchItems))
	if err != nil {
		failure(w, 400, "invalid_limit")
		return
	}
	wait, err := number(q.Get("wait"), 0, 0, int64(s.cfg.Limits.LongPollSeconds))
	if err != nil {
		failure(w, 400, "invalid_wait")
		return
	}
	if wait > 0 {
		if !s.reservePoll(key, r.PathValue("mailbox")) {
			failure(w, 429, "poll_limit")
			return
		}
		defer s.releasePoll(key, r.PathValue("mailbox"))
		select {
		case s.polls <- struct{}{}:
			defer func() { <-s.polls }()
		default:
			failure(w, 503, "poll_backpressure")
			return
		}
	}
	timer := time.NewTimer(time.Duration(wait) * time.Second)
	defer timer.Stop()
	watch, unwatch := s.watchMailbox(r.PathValue("mailbox"))
	defer unwatch()
	for {
		changed := s.changes(watch)
		messages, fetchErr := s.db.Fetch(r.Context(), r.PathValue("mailbox"), key, after, int(limit), s.now())
		if fetchErr != nil {
			storageError(w, fetchErr)
			return
		}
		if len(messages) > 0 || wait == 0 {
			sendMessages(w, messages)
			return
		}
		releaseRequestSlot(r)
		select {
		case <-r.Context().Done():
			failure(w, 503, "request_cancelled")
			return
		case <-s.ctx.Done():
			failure(w, 503, "shutdown")
			return
		case <-timer.C:
			sendMessages(w, messages)
			return
		case <-changed:
		}
	}
}

func sendMessages(w http.ResponseWriter, messages []store.Message) {
	items := make([]fetched, 0, len(messages))
	for _, m := range messages {
		items = append(items, fetched{Seq: m.Seq, Packet: m.Packet, Tombstone: m.Tombstone})
	}
	reply(w, 200, map[string]any{"messages": items})
}

func (s *Server) ack(w http.ResponseWriter, r *http.Request, key string, body []byte) {
	var input struct {
		Through int64 `json:"through"`
	}
	if !decode(w, body, &input) {
		return
	}
	if input.Through < 0 || input.Through > protocol.MaxSafeInteger {
		failure(w, 400, "invalid_cursor")
		return
	}
	if err := s.db.Ack(r.Context(), r.PathValue("mailbox"), key, input.Through, s.now()); err != nil {
		storageError(w, err)
		return
	}
	reply(w, 200, map[string]int64{"acked": input.Through})
}

func number(value string, fallback, low, high int64) (int64, error) {
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < low || n > high || strconv.FormatInt(n, 10) != value {
		return 0, errQuery
	}
	return n, nil
}
