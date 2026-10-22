package store

import "context"

// InboxEntry resolves a reply parent by its exact committed ID. A retained
// message beyond an arbitrary list page is still a valid parent; control,
// parked and expired rows cannot supply reply authority.
func (s *Store) InboxEntry(ctx context.Context, id string) (InboxEntry, error) {
	var item InboxEntry
	item.MessageID = id
	err := s.db.QueryRowContext(ctx, `SELECT i.grant_id,i.body,i.route,i.state,i.conversation_id,i.parent_id,i.budget_id,i.hop FROM inbox i JOIN grants g ON g.id=i.grant_id WHERE i.id=? AND (i.state='pending' OR (i.route='inbox_no_wake' AND i.state IN ('offered','delivered'))) AND i.route!='responder' AND i.lane='data' AND i.expires_at>? AND g.revoked=0 AND g.expires_at>? AND i.epoch=g.epoch`, id, s.clock().Unix(), s.clock().Unix()).Scan(&item.GrantID, &item.Body, &item.Routing, &item.State, &item.ConversationID, &item.ParentID, &item.BudgetID, &item.Hop)
	if err != nil {
		return item, lookupError(err)
	}
	item.Body, err = s.unseal("inbox", id, item.Body)
	return item, err
}
