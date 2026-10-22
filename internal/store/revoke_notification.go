package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func (s *Store) revokeReceiptOutbox(ctx context.Context, tx *sql.Tx, grantID string, builder ReceiptBuilder, r protocol.ReceiptPayload) error {
	raw, err := builder(r)
	if err != nil {
		return failure(ReceiptUnavailable)
	}
	p, err := protocol.DecodePacket(raw)
	if err != nil {
		return err
	}
	if p.Header.MessageID == r.MessageID || p.Header.ExpiresAt <= r.At {
		return failure(fault.InvalidEncoding)
	}
	raw, err = codec.Canonical(p)
	if err != nil {
		return err
	}
	if err = s.putRevokeOutbox(ctx, tx, p.Header.MessageID, grantID, raw, p.Header.ExpiresAt, r.At); err != nil {
		return err
	}
	return s.recordReceipt(ctx, tx, InboundReceipt, grantID, r)
}

// OutboxIsControl distinguishes receipts from data without opening peer content.
func (s *Store) OutboxIsControl(ctx context.Context, id string) (bool, error) {
	var lane string
	if err := s.db.QueryRowContext(ctx, `SELECT lane FROM outbox WHERE id=? AND stage='sender_stored'`, id).Scan(&lane); err != nil {
		return false, lookupError(err)
	}
	return lane == "control", nil
}

// StopRevokedReceipt stops transport retries without claiming remote receipt.
// The exact control channel must match a locally revoked incoming grant; a
// relay denial alone cannot terminate live peer data or change grant authority.
func (s *Store) StopRevokedReceipt(ctx context.Context, id, revokedGrant string, now int64) error {
	if !protocol.ValidID(id) || !protocol.ValidToken(revokedGrant) || now <= 0 || now > protocol.MaxSafeInteger {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var stage, digest string
		var epoch uint64
		err := tx.QueryRowContext(ctx, `SELECT o.stage,o.digest,g.epoch FROM outbox o JOIN grants g ON g.id=? WHERE o.id=? AND o.lane='control' AND g.revoked=1 AND o.channel=g.issuer||':'||g.subject`, revokedGrant, id).Scan(&stage, &digest, &epoch)
		if err != nil {
			return lookupError(err)
		}
		if stage != "sender_stored" {
			return nil
		}
		if _, err = tx.ExecContext(ctx, `UPDATE outbox SET stage='rejected' WHERE id=? AND stage='sender_stored'`, id); err != nil {
			return failure(StorageUnavailable)
		}
		return s.audit(ctx, tx, now, "revoke_notification_undeliverable", revokedGrant, epoch, id, digest, "route_closed")
	})
}
