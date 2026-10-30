package daemon

import (
	"path/filepath"

	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
)

// ProfileID compares local routing choices without exposing the state path.
// It is not a capability, identity proof or relay-visible identifier.
func ProfileID(stateDir, relay string) string {
	return peercrypto.Digest([]byte(filepath.Clean(stateDir) + "\x00" + relay))
}
