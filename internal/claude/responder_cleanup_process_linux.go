package claude

import (
	"io/fs"
	"os"
	"syscall"
)

func responderForkProcessOwner(info fs.FileInfo) (bool, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, &ResponderError{Code: ForkCleanupFailed}
	}
	return int64(stat.Uid) == int64(os.Getuid()), nil
}
