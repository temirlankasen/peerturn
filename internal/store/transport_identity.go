package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/identity"
)

// InstallSession is an explicit trusted initialization operation. It verifies the
// pinned chain before persistence; it grants no peer access or native binding.
func (s *Store) InstallSession(ctx context.Context, chain identity.Chain, pin string, now int64) error {
	cert, err := identity.VerifyChain(chain, pin, now)
	if err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		return installSession(ctx, tx, chain, cert)
	})
}
