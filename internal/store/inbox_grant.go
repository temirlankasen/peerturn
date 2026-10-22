package store

import "context"

// InboxGrant resolves a retained receipt target even after native admission.
// It supplies no authority; the daemon must verify the reverse receipt grant.
func (s *Store) InboxGrant(ctx context.Context, id string) (string, error) {
	var grantID string
	err := s.db.QueryRowContext(ctx, `SELECT grant_id FROM inbox WHERE id=? AND lane='data'`, id).Scan(&grantID)
	if err != nil {
		return "", lookupError(err)
	}
	return grantID, nil
}
