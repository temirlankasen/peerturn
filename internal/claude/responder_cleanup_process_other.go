//go:build !linux

package claude

import "io/fs"

func responderForkProcessOwner(fs.FileInfo) (bool, error) {
	return false, &ResponderError{Code: ForkCleanupFailed}
}
