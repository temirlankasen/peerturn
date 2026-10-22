package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// NativeItem retains the signed encrypted packet so framing can use authenticated
// metadata rather than reconstructing peer authority from display text.
type NativeItem struct {
	MessageID string
	GrantID   string
	Routing   envelope.Routing
	Packet    []byte
	Grant     grant.Grant
	GrantHash string
	Error     error
}

// NativeNotification is an immutable receipt packet signed by the local daemon.
// Its reciprocal grant is distinct from the received message's grant.
type NativeNotification struct {
	GrantID string
	Packet  []byte
}

// NativePending excludes revoked, expired and already attempted input. Ordering
// follows sender sequence even when out-of-order relay slots were parked first.
func (s *Store) NativePending(ctx context.Context, limit int) ([]NativeItem, error) {
	return s.NativePendingRoute(ctx, limit, "")
}

// NativePendingRoute keeps an unattended inform backlog from starving live input.
func (s *Store) NativePendingRoute(ctx context.Context, limit int, route envelope.Routing) ([]NativeItem, error) {
	if limit < 1 || limit > 100 {
		return nil, failure(fault.InvalidEncoding)
	}
	if route != "" && route != envelope.Live && route != envelope.InboxNoWake && route != envelope.Responder {
		return nil, failure(fault.InvalidEncoding)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.grant_id,i.route,i.packet,g.signed,g.hash FROM inbox i JOIN grants g ON g.id=i.grant_id WHERE i.state='pending' AND i.lane='data' AND i.expires_at>? AND g.revoked=0 AND g.expires_at>? AND i.epoch=g.epoch AND (?='' OR i.route=?) ORDER BY i.channel,i.sender_seq LIMIT ?`, s.clock().Unix(), s.clock().Unix(), route, route, limit)
	if err != nil {
		return nil, failure(StorageUnavailable)
	}
	var result []NativeItem
	var grants [][]byte
	for rows.Next() {
		var item NativeItem
		var signed []byte
		if err = rows.Scan(&item.MessageID, &item.GrantID, &item.Routing, &item.Packet, &signed, &item.GrantHash); err != nil {
			break
		}
		result = append(result, item)
		grants = append(grants, signed)
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return nil, failure(StorageUnavailable)
	}
	for i := range result {
		item := &result[i]
		item.Packet, item.Error = s.unseal("inbox_packet", item.MessageID, item.Packet)
		if item.Error != nil {
			continue
		}
		raw, openErr := s.unseal("grants", item.GrantID, grants[i])
		if openErr != nil {
			item.Error = openErr
			continue
		}
		item.Grant, item.Error = catalogGrant(string(raw))
		if item.Error == nil && (item.Grant.GrantID != item.GrantID || grant.Hash(string(raw)) != item.GrantHash) {
			item.Error = failure(InvalidState)
		}
	}
	return result, nil
}

// NativePendingCount reports the same shared no-wake backlog for native adapters.
func (s *Store) NativePendingCount(ctx context.Context) (int, error) {
	return s.PendingInformCount(ctx)
}

func (s *Store) nativeNotification(ctx context.Context, tx *sql.Tx, grantID string, r protocol.ReceiptPayload, n NativeNotification) error {
	p, err := protocol.DecodePacket(n.Packet)
	if err != nil {
		return err
	}
	if p.Header.MessageID == r.MessageID || p.Header.ExpiresAt <= r.At {
		return failure(fault.InvalidEncoding)
	}
	var reciprocal bool
	if err = tx.QueryRowContext(ctx, `SELECT a.issuer=b.subject AND a.subject=b.issuer FROM grants a JOIN grants b ON b.id=? WHERE a.id=?`, grantID, n.GrantID).Scan(&reciprocal); err != nil {
		return lookupError(err)
	}
	if !reciprocal {
		return failure(fault.ScopeDenied)
	}
	if _, err = s.authorization(ctx, tx, n.GrantID, r.At); err != nil {
		return err
	}
	_, err = s.putOutbox(ctx, tx, p.Header.MessageID, n.GrantID, n.Packet, "control", p.Header.ExpiresAt, r.At)
	return err
}
