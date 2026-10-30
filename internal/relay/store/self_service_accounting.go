package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
)

// The retained v2 links classify custody even after expiry, block or root
// deletion. UNION prevents double charging a mailbox with multiple origins.
const selfServiceCustody = `WITH self_mailboxes(id) AS (
 SELECT m.id FROM mailboxes m JOIN json_each(?) own ON m.id=own.value
 UNION
 SELECT m.id FROM mailboxes m JOIN invites i ON i.enrolled_mailbox=m.id
 WHERE i.inviter_key IS NOT NULL
)
SELECT
 (SELECT COUNT(*) FROM relay_inviters)+
 (SELECT COUNT(*) FROM invites WHERE inviter_key IS NOT NULL)+
 (SELECT COUNT(*) FROM self_mailboxes)+
 (SELECT COUNT(*) FROM permits WHERE mailbox_id IN (SELECT id FROM self_mailboxes))+
 (SELECT COUNT(*) FROM messages WHERE mailbox_id IN (SELECT id FROM self_mailboxes))+
 (SELECT COUNT(*) FROM tickets WHERE mailbox_id IN (SELECT id FROM self_mailboxes)),
 COALESCE((SELECT SUM(COALESCE(LENGTH(packet),0)+COALESCE(LENGTH(claim_packet),0))
 FROM invites WHERE inviter_key IS NOT NULL),0)+
 COALESCE((SELECT SUM(LENGTH(packet)) FROM messages
 WHERE mailbox_id IN (SELECT id FROM self_mailboxes)),0)`

type selfServiceUsage struct {
	items        int64
	chargedBytes int64
}

func selfServiceCharge(ctx context.Context, tx *sql.Tx) (selfServiceUsage, error) {
	owned, err := selfServiceOwnMailboxes(ctx, tx)
	if err != nil {
		return selfServiceUsage{}, err
	}
	if len(owned) == 0 {
		return selfServiceUsage{}, nil
	}
	// This bound JSON array is transient query input, never a stored ownership
	// field. Own mailbox IDs are derived from the existing registry on each read.
	encoded, err := json.Marshal(owned)
	if err != nil {
		return selfServiceUsage{}, unavailable()
	}
	var items, packetBytes int64
	if err := tx.QueryRowContext(ctx, selfServiceCustody, string(encoded)).Scan(&items, &packetBytes); err != nil {
		return selfServiceUsage{}, unavailable()
	}
	return selfServiceUsage{items: items, chargedBytes: items*rowCharge + packetBytes}, nil
}

func selfServiceOwnMailboxes(ctx context.Context, tx *sql.Tx) (owned []string, resultErr error) {
	rows, err := tx.QueryContext(ctx, `SELECT routing_key FROM relay_inviters`)
	if err != nil {
		return nil, unavailable()
	}
	defer func() {
		if err := rows.Close(); err != nil && resultErr == nil {
			owned, resultErr = nil, unavailable()
		}
	}()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, unavailable()
		}
		// Accounting derives addresses, not authority. Admission separately
		// requires strict canonical encoding and caller authentication.
		raw, err := base64.RawURLEncoding.DecodeString(key)
		if err != nil || len(raw) != 32 {
			return nil, unavailable()
		}
		owned = append(owned, hex.EncodeToString(digest(raw)))
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable()
	}
	return owned, nil
}

func (s *Store) transactSelfServiceBudget(ctx context.Context, tx *sql.Tx, fn func(*sql.Tx) error) error {
	before, err := selfServiceCharge(ctx, tx)
	if err != nil {
		return err
	}
	if mutationErr := fn(tx); mutationErr != nil {
		return mutationErr
	}
	after, err := selfServiceCharge(ctx, tx)
	if err != nil {
		return err
	}
	// Non-growing control operations and exact retries remain available under
	// pressure, including when an operator reopens with a smaller custody budget.
	if after.items > before.items && after.items > int64(s.metadataCapacity()/4) {
		return quota()
	}
	if after.chargedBytes > before.chargedBytes && after.chargedBytes > s.chargedCap(4) {
		return quota()
	}
	return nil
}
