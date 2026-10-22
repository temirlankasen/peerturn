package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuthorityMigrationRefusesTruncatedDatabaseWithoutReplacingKey(t *testing.T) {
	dir := fileAuthorityFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "state.db"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	backend := &memoryKeyStore{}
	if err := MigrateAuthoritySecrets(t.Context(), dir, backend); err == nil {
		t.Fatal("empty database replaced during key migration")
	}
	info, err := os.Stat(filepath.Join(dir, "seal.key"))
	if err != nil || info.Size() != 32 {
		t.Fatal("old key was removed after losing database state", err)
	}
}

func TestAuthorityMigrationRepeatRefusesMissingDatabase(t *testing.T) {
	dir := fileAuthorityFixture(t)
	backend := &memoryKeyStore{}
	if err := MigrateAuthoritySecrets(t.Context(), dir, backend); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.db")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := MigrateAuthoritySecrets(t.Context(), dir, backend); err == nil {
		t.Fatal("repeat migration recreated a missing database")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("missing database was silently replaced", err)
	}
}
