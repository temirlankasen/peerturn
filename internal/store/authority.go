package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
)

// InstallGrant is an explicit trusted owner operation, never discovery/auto-attach.
// M1a verifies both pinned chains and the recipient signature before persistence.
func (s *Store) InstallGrant(ctx context.Context, o envelope.OpenOptions) error {
	r, err := identity.VerifyChain(o.RecipientChain, o.RecipientPin, o.Now)
	if err != nil {
		return err
	}
	p, err := identity.VerifyChain(o.SenderChain, o.SenderPin, o.Now)
	if err != nil {
		return err
	}
	g, err := grant.Verify(o.SignedGrant, r, p, o.CurrentGrantEpoch, o.Now)
	if err != nil {
		return err
	}
	sealed, err := s.seal("grants", g.GrantID, []byte(o.SignedGrant))
	if err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var hash string
		err := tx.QueryRowContext(ctx, `SELECT hash FROM grants WHERE id=?`, g.GrantID).Scan(&hash)
		if err == nil {
			if hash != grant.Hash(o.SignedGrant) {
				return failure(fault.Conflict)
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return failure(StorageUnavailable)
		}
		for _, entry := range []struct {
			chain identity.Chain
			cert  identity.SessionCertificate
		}{{o.RecipientChain, r}, {o.SenderChain, p}} {
			if err = installSession(ctx, tx, entry.chain, entry.cert); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO grants(id,issuer,subject,signed,hash,epoch,expires_at) VALUES(?,?,?,?,?,?,?)`, g.GrantID, bindingKey(r.Binding), bindingKey(p.Binding), sealed, grant.Hash(o.SignedGrant), g.GrantEpoch, g.ExpiresAt)
		if err != nil {
			return failure(StorageUnavailable)
		}
		return s.audit(ctx, tx, o.Now, "grant_install", g.GrantID, g.GrantEpoch, "", "", "")
	})
}

func installSession(ctx context.Context, tx *sql.Tx, c identity.Chain, cert identity.SessionCertificate) error {
	deviceID := peercrypto.Digest([]byte(c.Device))
	raw, err := codec.Canonical(c)
	if err != nil {
		return err
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT OR IGNORE INTO identities VALUES(?,?)`, []any{cert.IdentityID, c.RootPublicKey}},
		{`INSERT OR IGNORE INTO devices VALUES(?,?,?)`, []any{deviceID, cert.IdentityID, c.Device}},
		{`INSERT OR IGNORE INTO sessions(id,device_id,certificate) VALUES(?,?,?)`, []any{bindingKey(cert.Binding), deviceID, string(raw)}},
	} {
		if _, err = tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return failure(StorageUnavailable)
		}
	}
	return nil
}

func bindingKey(b identity.Binding) string {
	raw, err := codec.Canonical(b)
	if err != nil {
		return ""
	}
	return peercrypto.Digest(raw)
}

func channelKey(sender, recipient identity.Binding) string {
	return bindingKey(sender) + ":" + bindingKey(recipient)
}

// BindSession records an explicitly verified native execution context. A changed
// context requires a new session/epoch instead of inheriting the previous authority.
func (s *Store) BindSession(ctx context.Context, b identity.Binding, nativeContext []byte) error {
	if err := identity.ValidateBinding(b); err != nil {
		return err
	}
	if len(nativeContext) == 0 || len(nativeContext) > 4096 {
		return failure(fault.InvalidEncoding)
	}
	id := bindingKey(b)
	sealed, err := s.seal("bindings", id, nativeContext)
	if err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var old []byte
		err := tx.QueryRowContext(ctx, `SELECT context FROM bindings WHERE session_id=?`, id).Scan(&old)
		if err == nil {
			plain, openErr := s.unseal("bindings", id, old)
			if openErr != nil {
				return openErr
			}
			if string(plain) != string(nativeContext) {
				return failure(fault.Conflict)
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return failure(StorageUnavailable)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO bindings VALUES(?,?,'bound')`, id, sealed)
		return databaseError(err)
	})
}

func (s *Store) authorization(ctx context.Context, tx *sql.Tx, id string, now int64) (grant.Grant, error) {
	var sealed []byte
	var issuer, subject string
	var epoch uint64
	var revoked bool
	err := tx.QueryRowContext(ctx, `SELECT signed,issuer,subject,epoch,revoked FROM grants WHERE id=?`, id).Scan(&sealed, &issuer, &subject, &epoch, &revoked)
	if err != nil {
		return grant.Grant{}, lookupError(err)
	}
	if revoked {
		return grant.Grant{}, failure(fault.Revoked)
	}
	r, err := storedSession(ctx, tx, issuer, now)
	if err != nil {
		return grant.Grant{}, err
	}
	p, err := storedSession(ctx, tx, subject, now)
	if err != nil {
		return grant.Grant{}, err
	}
	raw, err := s.unseal("grants", id, sealed)
	if err != nil {
		return grant.Grant{}, err
	}
	return grant.Verify(string(raw), r, p, epoch, now)
}

func storedSession(ctx context.Context, tx *sql.Tx, id string, now int64) (identity.SessionCertificate, error) {
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT certificate FROM sessions WHERE id=?`, id).Scan(&raw); err != nil {
		return identity.SessionCertificate{}, lookupError(err)
	}
	var c identity.Chain
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return identity.SessionCertificate{}, failure(InvalidState)
	}
	return identity.VerifyChain(c, identity.RootFingerprint(c.RootPublicKey), now)
}
