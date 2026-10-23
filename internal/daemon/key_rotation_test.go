package daemon

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/keystore"
	"github.com/temirlankasen/peerturn/internal/store"
)

type memoryKeyStore struct {
	keys        map[string][]byte
	unavailable bool
}

func (b *memoryKeyStore) Load(_ context.Context, id string) ([]byte, error) {
	if b.unavailable || b.keys[id] == nil {
		return nil, keystore.ErrUnavailable
	}
	return bytes.Clone(b.keys[id]), nil
}
func (b *memoryKeyStore) Store(_ context.Context, id string, key []byte) error {
	if b.unavailable || b.keys[id] != nil {
		return keystore.ErrUnavailable
	}
	if b.keys == nil {
		b.keys = map[string][]byte{}
	}
	b.keys[id] = bytes.Clone(key)
	return nil
}

func fileAuthorityFixture(t *testing.T) string {
	t.Helper()
	dir := stateDir(t)
	if _, err := Initialize(t.Context(), InitOptions{StateDir: dir, RelayURL: "https://relay.example", SessionLabel: "rotation", FilePermissions: true}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAuthorityMigrationUsesFreshKeyAndRefusesStaleFileKey(t *testing.T) {
	parallelDaemonTest(t)
	dir := fileAuthorityFixture(t)
	root, err := store.OpenPrivateDirectory(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	oldKey, err := root.ReadFile("seal.key")
	if err != nil {
		t.Fatal(err)
	}
	defer clear(oldKey)
	if err = root.Link("seal.key", "old-key-alias"); err != nil {
		t.Fatal(err)
	}
	old, err := store.NewKeySealer(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	backend := &memoryKeyStore{}
	if err = MigrateAuthoritySecrets(t.Context(), dir, backend); err != nil {
		t.Fatal(err)
	}
	metadata, err := readWrappingMetadata(dir)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(oldKey, backend.keys[metadata.KeyID]) {
		t.Fatal("migration moved old key")
	}
	alias, err := root.ReadFile("old-key-alias")
	if err != nil || !bytes.Equal(alias, make([]byte, 32)) {
		t.Fatal("original key inode alias was not overwritten")
	}
	if _, err = loadState(dir, old); err == nil {
		t.Fatal("old wrapping key opens migrated journal")
	}
	d, err := Open(t.Context(), Options{StateDir: dir, KeyStore: backend})
	if err != nil {
		t.Fatal(err)
	}
	if !d.authoritySecretsIsolated || d.authoritySecretsReason != "" {
		t.Fatal("isolated state not reported")
	}
	if err = d.checkAuthorityKey(t.Context()); err != nil {
		t.Fatal(err)
	}
	backend.unavailable = true
	if err = d.checkAuthorityKey(t.Context()); err == nil {
		t.Fatal("lost keystore accepted")
	}
	backend.unavailable = false
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	if err = root.WriteFile("seal.key", oldKey, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(t.Context(), Options{StateDir: dir, KeyStore: backend}); !fault.IsCode(err, store.SealingFailed) {
		t.Fatalf("stale file key accepted: %v", err)
	}
}

func TestAuthorityMigrationRecoversEveryDurableBoundary(t *testing.T) {
	parallelDaemonTest(t)
	for _, boundary := range []string{"prepared", "database", "journal", "verified", "wiped", "removed"} {
		t.Run(boundary, func(t *testing.T) {
			dir := fileAuthorityFixture(t)
			backend := &memoryKeyStore{}
			metadata, target, err := newWrappingKey(t.Context(), backend)
			if err != nil {
				t.Fatal(err)
			}
			if err = writeWrappingMetadata(dir, metadata, true); err != nil {
				t.Fatal(err)
			}
			if boundary != "prepared" {
				injected := errors.New("migration interrupted")
				err = finishAuthorityMigration(t.Context(), dir, metadata, target, backend, func(step string) error {
					if step == boundary {
						return injected
					}
					return nil
				})
				if !errors.Is(err, injected) {
					t.Fatalf("boundary was not reached: %v", err)
				}
			}
			d, err := Open(t.Context(), Options{StateDir: dir, KeyStore: backend})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err = d.Close(); err != nil {
					t.Error(err)
				}
			}()
			if !d.authoritySecretsIsolated {
				t.Fatal("recovered without isolation")
			}
			if _, err = os.Lstat(filepath.Join(dir, "seal.key")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("file key remains")
			}
			if err = d.db.VerifySealer(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAuthorityMigrationFailureRetainsFileKey(t *testing.T) {
	parallelDaemonTest(t)
	dir := fileAuthorityFixture(t)
	backend := &memoryKeyStore{unavailable: true}
	if err := MigrateAuthoritySecrets(t.Context(), dir, backend); err == nil {
		t.Fatal("missing service accepted")
	}
	if _, err := store.OpenFileSealer(filepath.Join(dir, "seal.key")); err != nil {
		t.Fatal("migration destroyed fallback")
	}
	d, err := Open(t.Context(), Options{StateDir: dir, KeyStore: backend})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err = d.Close(); err != nil {
			t.Error(err)
		}
	}()
	if d.authoritySecretsIsolated || d.authoritySecretsReason != "authority_file_key" {
		t.Fatal("file fallback claimed isolation")
	}
}

func TestInitializeOSAuthorityNeverWritesFileKey(t *testing.T) {
	parallelDaemonTest(t)
	dir := stateDir(t)
	backend := &memoryKeyStore{}
	if _, err := Initialize(t.Context(), InitOptions{StateDir: dir, RelayURL: "https://relay.example", SessionLabel: "system", KeyStore: backend}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "seal.key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("OS initialization wrote file key")
	}
	d, err := Open(t.Context(), Options{StateDir: dir, KeyStore: backend})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityMigrationCorruptDatabaseNeverDeletesFallbackKey(t *testing.T) {
	parallelDaemonTest(t)
	dir := fileAuthorityFixture(t)
	path := filepath.Join(dir, "state.db")
	if err := os.WriteFile(path, []byte("corrupt database"), 0600); err != nil {
		t.Fatal(err)
	}
	backend := &memoryKeyStore{}
	if err := MigrateAuthoritySecrets(t.Context(), dir, backend); err == nil {
		t.Fatal("corrupt database accepted")
	}
	if _, err := store.OpenFileSealer(filepath.Join(dir, "seal.key")); err != nil {
		t.Fatal("failed migration deleted fallback key")
	}
	if _, err := openAuthorityKey(t.Context(), dir, backend); err == nil {
		t.Fatal("failed recovery reported isolation")
	}
}
