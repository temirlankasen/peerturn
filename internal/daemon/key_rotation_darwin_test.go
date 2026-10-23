//go:build darwin

package daemon

import (
	"bytes"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestMacOSPendingAuthorityMigrationRefusesBeforeRecovery(t *testing.T) {
	parallelDaemonTest(t)
	dir := fileAuthorityFixture(t)
	backend := &memoryKeyStore{}
	metadata, _, err := newWrappingKey(t.Context(), backend)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeWrappingMetadata(dir, metadata, true); err != nil {
		t.Fatal(err)
	}
	before := authorityFixtureKey(t, dir)
	defer clear(before)
	for _, action := range []func() error{
		func() error { return MigrateAuthoritySecrets(t.Context(), dir, nil) },
		func() error { _, openErr := openAuthorityKey(t.Context(), dir, nil); return openErr },
	} {
		if err = action(); localipc.PublicError(err).Code != "macos_unqualified" {
			t.Fatalf("pending migration reached unqualified keychain: %v", err)
		}
		after := authorityFixtureKey(t, dir)
		if !bytes.Equal(before, after) {
			t.Fatal("unqualified recovery changed file key")
		}
		clear(after)
		pending, readErr := readWrappingMetadata(dir)
		if readErr != nil || pending != metadata {
			t.Fatal("unqualified recovery changed pending migration", readErr)
		}
	}
}
