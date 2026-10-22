package daemon

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/keystore"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/store"
)

type authorityKey struct {
	sealer     store.Sealer
	backend    keystore.Backend
	id, reason string
	isolated   bool
}

func createAuthorityKey(ctx context.Context, o InitOptions) (store.Sealer, error) {
	if _, err := readWrappingMetadata(o.StateDir); !errors.Is(err, os.ErrNotExist) {
		return nil, fault.New(store.SealingFailed)
	}
	if o.FilePermissions {
		return store.CreateFileSealer(filepath.Join(o.StateDir, "seal.key"))
	}
	if err := refuseFileKey(o.StateDir); err != nil {
		return nil, err
	}
	metadata, sealer, err := newWrappingKey(ctx, o.KeyStore)
	if err != nil {
		return nil, err
	}
	metadata.Phase = "active"
	if err = writeWrappingMetadata(o.StateDir, metadata, true); err != nil {
		return nil, err
	}
	return sealer, nil
}

func newWrappingKey(ctx context.Context, backend keystore.Backend) (wrappingMetadata, store.Sealer, error) {
	metadata := wrappingMetadata{Version: 1, KeyID: randomID(), Phase: "pending"}
	var key [32]byte
	defer clear(key[:])
	if _, err := rand.Read(key[:]); err != nil {
		return metadata, nil, fault.New(store.SealingFailed)
	}
	if backend == nil {
		return metadata, nil, keystore.ErrUnavailable
	}
	if err := backend.Store(ctx, metadata.KeyID, key[:]); err != nil {
		return metadata, nil, keystore.ErrUnavailable
	}
	check, err := backend.Load(ctx, metadata.KeyID)
	defer clear(check)
	if err != nil || subtle.ConstantTimeCompare(key[:], check) != 1 {
		return metadata, nil, keystore.ErrUnavailable
	}
	sealer, err := store.NewKeySealer(key[:])
	return metadata, sealer, err
}

func openAuthorityKey(ctx context.Context, dir string, backend keystore.Backend) (authorityKey, error) {
	metadata, err := readWrappingMetadata(dir)
	if errors.Is(err, os.ErrNotExist) {
		sealer, openErr := store.OpenFileSealer(filepath.Join(dir, "seal.key"))
		return authorityKey{sealer: sealer, reason: "authority_file_key"}, openErr
	}
	if err != nil {
		return authorityKey{}, err
	}
	if backend == nil {
		if metadata.Phase == "pending" {
			backend, err = authorityKeyBackend(nil)
			if err != nil {
				return authorityKey{}, err
			}
		} else {
			backend = keystore.System()
		}
	}
	key, err := backend.Load(ctx, metadata.KeyID)
	defer clear(key)
	if err != nil {
		return authorityKey{}, keystore.ErrUnavailable
	}
	sealer, err := store.NewKeySealer(key)
	if err != nil {
		return authorityKey{}, err
	}
	if metadata.Phase == "pending" {
		if err = finishAuthorityMigration(ctx, dir, metadata, sealer, backend, nil); err != nil {
			return authorityKey{}, err
		}
	} else if err = refuseFileKey(dir); err != nil {
		return authorityKey{}, err
	}
	return authorityKey{sealer: sealer, backend: backend, id: metadata.KeyID, isolated: true}, nil
}

// MigrateAuthoritySecrets must run while the daemon is stopped. It generates a
// new OS key, reseals and verifies the DB and journal, then removes the file key.
// A pending marker is recovered on qualified Open; active forbids stale file keys.
func MigrateAuthoritySecrets(ctx context.Context, dir string, backend keystore.Backend) (result error) {
	backend, err := authorityKeyBackend(backend)
	if err != nil {
		return err
	}
	lock, err := localipc.LockStateDir(dir, false)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	_, err = readWrappingMetadata(dir)
	if err == nil {
		authority, openErr := openAuthorityKey(ctx, dir, backend)
		if openErr != nil {
			return openErr
		}
		if _, openErr = loadState(dir, authority.sealer); openErr != nil {
			return openErr
		}
		db, openErr := store.Open(ctx, filepath.Join(dir, "state.db"), store.Options{Sealer: authority.sealer, RequireExistingSchema: true})
		if openErr != nil {
			return openErr
		}
		return errors.Join(db.VerifySealer(ctx), db.Close())
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	old, err := store.OpenFileSealer(filepath.Join(dir, "seal.key"))
	if err != nil {
		return err
	}
	if _, err = loadState(dir, old); err != nil {
		return err
	}
	metadata, sealer, err := newWrappingKey(ctx, backend)
	if err != nil {
		return err
	}
	if err = writeWrappingMetadata(dir, metadata, true); err != nil {
		return err
	}
	return finishAuthorityMigration(ctx, dir, metadata, sealer, backend, nil)
}

func authorityKeyBackend(backend keystore.Backend) (keystore.Backend, error) {
	if backend != nil {
		return backend, nil
	}
	// The keychain deputy has only mocked evidence. Refuse before contacting it,
	// including when a pending marker would otherwise resume an irreversible wipe.
	if runtime.GOOS == "darwin" {
		return nil, localipc.NewError("macos_unqualified", "rejected")
	}
	return keystore.System(), nil
}

type migrationSealer struct{ current, previous store.Sealer }

func (s migrationSealer) Seal(aad, plain []byte) ([]byte, error) { return s.current.Seal(aad, plain) }
func (s migrationSealer) Open(aad, sealed []byte) ([]byte, error) {
	plain, err := s.current.Open(aad, sealed)
	if err != nil && s.previous != nil {
		return s.previous.Open(aad, sealed)
	}
	return plain, err
}

func finishAuthorityMigration(ctx context.Context, dir string, metadata wrappingMetadata, target store.Sealer, backend keystore.Backend, abort func(string) error) error {
	info, err := os.Lstat(filepath.Join(dir, "state.db"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !ownedJournal(info) {
		return fault.New(store.SealingFailed)
	}
	var previous store.Sealer
	if _, statErr := os.Lstat(filepath.Join(dir, "seal.key")); statErr == nil {
		previous, err = store.OpenFileSealer(filepath.Join(dir, "seal.key"))
		if err != nil {
			return err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fault.New(store.SealingFailed)
	}
	source := migrationSealer{current: target, previous: previous}
	state, err := loadState(dir, source)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, filepath.Join(dir, "state.db"), store.Options{Sealer: source, RequireExistingSchema: true})
	if err != nil {
		return err
	}
	err = db.RotateSealer(ctx, target)
	if err = errors.Join(err, db.Close()); err != nil {
		return err
	}
	if err = migrationStep(abort, "database"); err != nil {
		return err
	}
	if err = saveState(dir, target, state, false); err != nil {
		return err
	}
	if err = migrationStep(abort, "journal"); err != nil {
		return err
	}
	if _, err = loadState(dir, target); err != nil {
		return err
	}
	db, err = store.Open(ctx, filepath.Join(dir, "state.db"), store.Options{Sealer: target, RequireExistingSchema: true})
	if err != nil {
		return err
	}
	err = db.VerifySealer(ctx)
	if err = errors.Join(err, db.Close()); err != nil {
		return err
	}
	if err = migrationStep(abort, "verified"); err != nil {
		return err
	}
	if err = verifyAuthorityKey(ctx, backend, metadata.KeyID, target); err != nil {
		return err
	}
	root, err := store.OpenPrivateDirectory(dir, false)
	if err != nil {
		return err
	}
	err = wipeFileKey(root)
	if err == nil {
		err = migrationStep(abort, "wiped")
	}
	if err == nil {
		err = root.Remove("seal.key")
	}
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err == nil {
		err = syncAuthorityDirectory(root)
	}
	if err = errors.Join(err, root.Close()); err != nil {
		return err
	}
	if err = migrationStep(abort, "removed"); err != nil {
		return err
	}
	metadata.Phase = "active"
	return writeWrappingMetadata(dir, metadata, false)
}

func migrationStep(abort func(string) error, step string) error {
	if abort != nil {
		return abort(step)
	}
	return nil
}

func refuseFileKey(dir string) error {
	if _, err := os.Lstat(filepath.Join(dir, "seal.key")); !errors.Is(err, os.ErrNotExist) {
		return fault.New(store.SealingFailed)
	}
	return nil
}

func (d *Daemon) checkAuthorityKey(ctx context.Context) error {
	if !d.authoritySecretsIsolated || d.authorityKeyStore == nil {
		return keystore.ErrUnavailable
	}
	metadata, err := readWrappingMetadata(d.options.StateDir)
	if err != nil || metadata.Phase != "active" || metadata.KeyID != d.authorityKeyID {
		return keystore.ErrUnavailable
	}
	if err = refuseFileKey(d.options.StateDir); err != nil {
		return err
	}
	return verifyAuthorityKey(ctx, d.authorityKeyStore, d.authorityKeyID, d.sealer)
}

func verifyAuthorityKey(ctx context.Context, backend keystore.Backend, id string, sealer store.Sealer) error {
	if backend == nil {
		return keystore.ErrUnavailable
	}
	key, err := backend.Load(ctx, id)
	defer clear(key)
	if err != nil {
		return keystore.ErrUnavailable
	}
	loaded, err := store.NewKeySealer(key)
	if err != nil {
		return err
	}
	aad := []byte("peerturn/daemon/v1/authority-check")
	sealed, err := sealer.Seal(aad, aad)
	if err != nil {
		return err
	}
	plain, err := loaded.Open(aad, sealed)
	defer clear(plain)
	if err != nil || subtle.ConstantTimeCompare(plain, aad) != 1 {
		return keystore.ErrUnavailable
	}
	return nil
}
