package daemon

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/temirlankasen/peerturn/internal/keystore"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/store"
)

type expiringKeyStore struct {
	memoryKeyStore
	loads, failAfter int
	changed          bool
}

func (b *expiringKeyStore) Load(ctx context.Context, id string) ([]byte, error) {
	b.loads++
	if b.loads > b.failAfter {
		if b.changed {
			return bytes.Repeat([]byte{23}, 32), nil
		}
		return nil, keystore.ErrUnavailable
	}
	return b.memoryKeyStore.Load(ctx, id)
}

func authorityFixtureKey(t *testing.T, directory string) []byte {
	t.Helper()
	root, err := store.OpenPrivateDirectory(directory, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := root.ReadFile("seal.key")
	if closeErr := root.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAuthorityMigrationRechecksKeyBeforeWipingFile(t *testing.T) {
	for _, name := range []string{"unavailable", "changed", "pending recovery"} {
		t.Run(name, func(t *testing.T) {
			dir := fileAuthorityFixture(t)
			before := authorityFixtureKey(t, dir)
			defer clear(before)
			backend := &expiringKeyStore{failAfter: 1, changed: name == "changed"}
			if name == "pending recovery" {
				backend.failAfter = 2
				metadata, _, prepareErr := newWrappingKey(t.Context(), backend)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				if err := writeWrappingMetadata(dir, metadata, true); err != nil {
					t.Fatal(err)
				}
			}
			if err := MigrateAuthoritySecrets(t.Context(), dir, backend); !errors.Is(err, keystore.ErrUnavailable) {
				t.Fatalf("lost service before key wipe accepted: %v", err)
			}
			after := authorityFixtureKey(t, dir)
			defer clear(after)
			if !bytes.Equal(before, after) {
				t.Fatal("file key changed after failed pre-wipe verification")
			}
			metadata, err := readWrappingMetadata(dir)
			if err != nil || metadata.Phase != "pending" {
				t.Fatal("failed key verification did not preserve pending recovery", err)
			}
		})
	}
}

func TestAuthorityMigrationRefusesRunningDaemonBeforeKeyStorage(t *testing.T) {
	dir := fileAuthorityFixture(t)
	d, err := Open(t.Context(), Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := d.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	backend := &memoryKeyStore{}
	if err = MigrateAuthoritySecrets(t.Context(), dir, backend); localipc.PublicError(err).Code != "already_running" {
		t.Fatalf("running daemon allowed migration: %v", err)
	}
	if len(backend.keys) != 0 {
		t.Fatal("locked state caused OS key storage")
	}
	if _, err = store.OpenFileSealer(filepath.Join(dir, "seal.key")); err != nil {
		t.Fatal("locked state lost file key", err)
	}
	if _, err = readWrappingMetadata(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("locked state acquired migration marker", err)
	}
}

func TestSystemAuthorityMigrationQualifiesBeforeStateAccess(t *testing.T) {
	want := "invalid_encoding"
	if runtime.GOOS == "darwin" {
		want = "macos_unqualified"
	}
	// A relative path prevents an unqualified implementation from contacting a
	// real keychain while still proving that platform refusal comes first.
	err := MigrateAuthoritySecrets(t.Context(), "relative-state", nil)
	if got := localipc.PublicError(err).Code; got != want {
		t.Fatalf("system key isolation qualification: got %s want %s", got, want)
	}
}
