// Package keystore stores wrapping keys outside file-readable authority state.
// It does not isolate keys from arbitrary same-uid code: Claude's admitted fork
// must have no process, socket, hook or MCP capability. Shell-capable forks need
// separate OS channel evidence. macOS security is a trusted keychain deputy.
package keystore

import (
	"context"
	"encoding/hex"
	"errors"
	"runtime"
)

// Backend loads existing keys and creates immutable, uniquely named new keys.
type Backend interface {
	Load(context.Context, string) ([]byte, error)
	Store(context.Context, string, []byte) error
}

// ErrUnavailable deliberately excludes service diagnostics and secret values.
var ErrUnavailable = errors.New("keystore_unavailable")

// System selects the OS service without accessing it until Load or Store.
func System() Backend {
	if runtime.GOOS == "darwin" {
		return securityBackend{run: runSecurity}
	}
	return secretServiceBackend{}
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
