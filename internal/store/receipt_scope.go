package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func receiptKey(direction ReceiptDirection, grantID, channel, id string) string {
	// Each component excludes '/', and channel consists of two fixed digests.
	return string(direction) + "/" + grantID + "/" + channel + "/" + id
}

func receiptScope(ctx context.Context, tx *sql.Tx, id string) (ReceiptDirection, string, error) {
	var direction ReceiptDirection
	var grantID string
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(direction,''),COALESCE(grant_id,''),COUNT(*) FROM (
 SELECT 'inbound' AS direction,grant_id FROM inbox WHERE id=?
 UNION ALL SELECT 'outbound',grant_id FROM outbox WHERE id=?
)`, id, id).Scan(&direction, &grantID, &count)
	if err != nil {
		return "", "", failure(StorageUnavailable)
	}
	if count == 0 {
		return "", "", failure(NotFound)
	}
	if count != 1 {
		return "", "", failure(fault.Conflict)
	}
	return direction, grantID, nil
}

func receiptChannel(ctx context.Context, tx *sql.Tx, direction ReceiptDirection, grantID, id string) (string, error) {
	query := `SELECT grant_id,channel FROM inbox WHERE id=?`
	if direction == OutboundReceipt {
		query = `SELECT grant_id,channel FROM outbox WHERE id=?`
	} else if direction != InboundReceipt {
		return "", failure(fault.InvalidEncoding)
	}
	var actualGrant, channel string
	if err := tx.QueryRowContext(ctx, query, id).Scan(&actualGrant, &channel); err != nil {
		return "", lookupError(err)
	}
	if actualGrant != grantID {
		return "", failure(fault.InvalidGrant)
	}
	if channel == "" {
		return "", failure(InvalidState)
	}
	return channel, nil
}

// acceptPeerReceipt binds authenticated ingress to an actual submission addressed
// to that exact peer. Shared display/session names are insufficient authority.
func (s *Store) acceptPeerReceipt(ctx context.Context, tx *sql.Tx, inboundGrantID string, r protocol.ReceiptPayload) error {
	var outboundGrant, channel, outIssuer, outSubject, inIssuer, inSubject string
	err := tx.QueryRowContext(ctx, `SELECT o.grant_id,o.channel,g.issuer,g.subject FROM outbox o JOIN grants g ON g.id=o.grant_id WHERE o.id=? AND o.lane='data'`, r.MessageID).Scan(&outboundGrant, &channel, &outIssuer, &outSubject)
	if err != nil {
		return lookupError(err)
	}
	if err = tx.QueryRowContext(ctx, `SELECT issuer,subject FROM grants WHERE id=?`, inboundGrantID).Scan(&inIssuer, &inSubject); err != nil {
		return lookupError(err)
	}
	if outIssuer != inSubject || outSubject != inIssuer || channel != inIssuer+":"+inSubject {
		return failure(fault.AudienceMismatch)
	}
	if r.EvidenceKind == "native_reconciliation" {
		var stage string
		if err = tx.QueryRowContext(ctx, `SELECT stage FROM receipts WHERE direction='outbound' AND grant_id=? AND channel=? AND message_id=?`, outboundGrant, channel, r.MessageID).Scan(&stage); err != nil {
			return lookupError(err)
		}
		if stage == "in_doubt" {
			return peerReceiptError(s.saveReceipt(ctx, tx, OutboundReceipt, outboundGrant, r, true))
		}
	}
	return peerReceiptError(s.recordReceipt(ctx, tx, OutboundReceipt, outboundGrant, r))
}

func peerReceiptError(err error) error {
	// Invalid peer evidence is a permanent item refusal, not a transient storage
	// failure that could stall this authenticated channel on every subsequent poll.
	if fault.IsCode(err, fault.Conflict) {
		return failure(fault.InvalidGrant)
	}
	return err
}
