package store

import (
	"context"
	"database/sql"
)

func (s *Store) initializeSchema(ctx context.Context) error {
	// Open configures BEGIN IMMEDIATE. Read the version under that same lock so
	// no partial schema or version advance escapes a failed migration.
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
			return unavailable()
		}
		switch version {
		case 0:
			if _, err := tx.ExecContext(ctx, baseSchema); err != nil {
				return unavailable()
			}
			fallthrough
		case 1:
			if _, err := tx.ExecContext(ctx, schemaV2); err != nil {
				return unavailable()
			}
		case 2:
			return nil
		default:
			return unavailable()
		}
		return nil
	})
}
