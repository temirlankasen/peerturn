package store

import (
	"context"
	"encoding/base64"
	"strings"

	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// OutboxItem carries the retained original packet, never a newly sealed retry.
type OutboxItem struct {
	MessageID string `json:"message_id"`
	GrantID   string `json:"grant_id"`
	Stage     string `json:"stage"`
	Packet    []byte `json:"packet"`
}

// InboxEntry exposes only admitted pending data and its committed chain metadata.
type InboxEntry struct {
	InboxItem
	GrantID        string `json:"grant_id"`
	ConversationID string `json:"conversation_id"`
	ParentID       string `json:"parent_id"`
	BudgetID       string `json:"budget_id"`
	Hop            uint64 `json:"hop"`
}

// GrantInfo is a catalog entry, not permission to enqueue or admit a message.
// Epoch and Revoked report live policy; the other fields describe the installed grant.
type GrantInfo struct {
	Hash      string           `json:"hash"`
	GrantID   string           `json:"grant_id"`
	Epoch     uint64           `json:"epoch"`
	Revoked   bool             `json:"revoked"`
	ExpiresAt int64            `json:"expires_at"`
	Issuer    identity.Binding `json:"issuer"`
	Subject   identity.Binding `json:"subject"`
	Scope     grant.Scope      `json:"scope"`
}

// PendingOutbox lists live sender custody with the independent control lane first.
// A transport result must be committed before it disappears from this list.
func (s *Store) PendingOutbox(ctx context.Context, limit int) ([]OutboxItem, error) {
	if limit <= 0 || limit > int(MaxMailboxLookahead) {
		return nil, failure(fault.InvalidEncoding)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,grant_id,stage,packet FROM outbox WHERE stage='sender_stored' AND expires_at>? ORDER BY CASE lane WHEN 'control' THEN 0 ELSE 1 END,priority DESC,created_at,id LIMIT ?`, s.clock().Unix(), limit)
	if err != nil {
		return nil, failure(StorageUnavailable)
	}
	var items []OutboxItem
	for rows.Next() {
		var item OutboxItem
		if err = rows.Scan(&item.MessageID, &item.GrantID, &item.Stage, &item.Packet); err != nil {
			break
		}
		items = append(items, item)
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return nil, failure(StorageUnavailable)
	}
	for i := range items {
		items[i].Packet, err = s.unseal("outbox", items[i].MessageID, items[i].Packet)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

// ListInbox returns literal local text without expansion, dispatch or model wake.
// Parked, control, expired and already attempted work cannot become pending here.
func (s *Store) ListInbox(ctx context.Context, limit int) ([]InboxEntry, error) {
	return s.ListInboxAfter(ctx, "", limit)
}

// ListInboxAfter continues strictly after the cursor's committed created_at/id
// tuple. An empty cursor selects the first page; missing or expired cursor rows
// yield an empty page rather than silently restarting from the beginning.
func (s *Store) ListInboxAfter(ctx context.Context, after string, limit int) ([]InboxEntry, error) {
	if limit <= 0 || limit > int(MaxMailboxLookahead) || after != "" && !protocol.ValidID(after) {
		return nil, failure(fault.InvalidEncoding)
	}
	now := s.clock().Unix()
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.grant_id,i.body,i.route,i.state,i.conversation_id,i.parent_id,i.budget_id,i.hop FROM inbox i JOIN grants g ON g.id=i.grant_id WHERE (i.state='pending' OR (i.route='inbox_no_wake' AND i.state IN ('offered','delivered'))) AND i.route!='responder' AND i.lane='data' AND i.expires_at>? AND g.revoked=0 AND g.expires_at>? AND i.epoch=g.epoch AND (?='' OR (i.created_at,i.id)>(SELECT created_at,id FROM inbox WHERE id=? AND expires_at>?)) ORDER BY i.created_at,i.id LIMIT ?`, now, now, after, after, now, limit)
	if err != nil {
		return nil, failure(StorageUnavailable)
	}
	var items []InboxEntry
	for rows.Next() {
		var item InboxEntry
		if err = rows.Scan(&item.MessageID, &item.GrantID, &item.Body, &item.Routing, &item.State, &item.ConversationID, &item.ParentID, &item.BudgetID, &item.Hop); err != nil {
			break
		}
		items = append(items, item)
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return nil, failure(StorageUnavailable)
	}
	for i := range items {
		items[i].Body, err = s.unseal("inbox", items[i].MessageID, items[i].Body)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

// ListGrants includes expired and revoked installations for owner inspection.
// Live authorization always goes through the transactional admission APIs.
func (s *Store) ListGrants(ctx context.Context) ([]GrantInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,epoch,revoked,expires_at,signed,hash,issuer,subject FROM grants ORDER BY id`)
	if err != nil {
		return nil, failure(StorageUnavailable)
	}
	type stored struct {
		info                  GrantInfo
		sealed                []byte
		hash, issuer, subject string
	}
	var records []stored
	for rows.Next() {
		var item stored
		if err = rows.Scan(&item.info.GrantID, &item.info.Epoch, &item.info.Revoked, &item.info.ExpiresAt, &item.sealed, &item.hash, &item.issuer, &item.subject); err != nil {
			break
		}
		records = append(records, item)
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return nil, failure(StorageUnavailable)
	}
	var items []GrantInfo
	for _, record := range records {
		raw, openErr := s.unseal("grants", record.info.GrantID, record.sealed)
		if openErr != nil {
			return nil, openErr
		}
		g, decodeErr := catalogGrant(string(raw))
		if decodeErr != nil || grant.Hash(string(raw)) != record.hash || g.GrantID != record.info.GrantID || g.ExpiresAt != record.info.ExpiresAt || bindingKey(g.Issuer) != record.issuer || bindingKey(g.Subject) != record.subject {
			return nil, failure(InvalidState)
		}
		record.info.Issuer, record.info.Subject, record.info.Scope = g.Issuer, g.Subject, g.Scope
		record.info.Hash = record.hash
		items = append(items, record.info)
	}
	return items, nil
}

func catalogGrant(compact string) (grant.Grant, error) {
	parts := strings.Split(compact, ".")
	if len(parts) != 3 {
		return grant.Grant{}, failure(InvalidState)
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return grant.Grant{}, failure(InvalidState)
	}
	g, err := grant.Decode(payload)
	if err != nil {
		return grant.Grant{}, err
	}
	// The local sealer authenticates this previously verified installation.
	// Recheck its signature without turning expiry or revocation into invisibility.
	if _, err = peercrypto.Verify(compact, identity.RootFingerprint(g.Issuer.SigningKey), peercrypto.GrantType, g.Issuer.SigningKey); err != nil {
		return grant.Grant{}, err
	}
	return g, nil
}
