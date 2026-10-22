package store

import (
	"context"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// RevokedOutboundGrant recovers authority from committed receipt evidence,
// including promoted parked receipts. These rows are written only by trusted
// local operations or the authenticated, reverse-channel-bound peer path;
// transport statuses cannot create a revoked receipt.
func (s *Store) RevokedOutboundGrant(ctx context.Context, grantID string) (bool, error) {
	if !protocol.ValidToken(grantID) {
		return false, failure(fault.InvalidEncoding)
	}
	var revoked bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM receipts WHERE direction='outbound' AND grant_id=? AND stage='revoked')`, grantID).Scan(&revoked)
	if err != nil {
		return false, failure(StorageUnavailable)
	}
	if revoked {
		return true, nil
	}
	// Settling a native outcome never restores authority revoked by the recipient.
	// Resolved receipts carry the original revocation through reconciliation.
	rows, err := s.db.QueryContext(ctx, `SELECT channel,message_id,evidence FROM receipts WHERE direction='outbound' AND grant_id=?`, grantID)
	if err != nil {
		return false, failure(StorageUnavailable)
	}
	for rows.Next() {
		var channel, messageID string
		var sealed []byte
		if err = rows.Scan(&channel, &messageID, &sealed); err != nil {
			break
		}
		var plain []byte
		plain, err = s.unseal("receipts", receiptKey(OutboundReceipt, grantID, channel, messageID), sealed)
		if err != nil {
			break
		}
		var receipt protocol.ReceiptPayload
		receipt, err = protocol.DecodeReceipt(plain)
		clear(plain)
		if err != nil {
			break
		}
		if receiptRevokesGrant(receipt) {
			revoked = true
			break
		}
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil {
		return false, err
	}
	if iterErr != nil || closeErr != nil {
		return false, failure(StorageUnavailable)
	}
	return revoked, nil
}

func receiptRevokesGrant(r protocol.ReceiptPayload) bool {
	if r.Stage == "revoked" {
		return true
	}
	if r.ReasonCode != "revoked" {
		return false
	}
	if r.Stage != "in_doubt" || r.EvidenceKind == "native_reconciliation" {
		return true
	}
	return r.EvidenceKind == "local_revoke" && (r.Outcome == "already_admitted" || r.Outcome == "submit_unknown")
}
