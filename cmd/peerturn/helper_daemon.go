package main

import (
	"path/filepath"

	"github.com/temirlankasen/peerturn/internal/daemon"
)

// helperDaemonStarter lets a waiting helper start the owner's daemon. Only a
// session that uses PeerTurn runs a helper, so after a reboot the bound session
// brings PeerTurn back by itself. Without a profile nothing starts: creating
// the first one is connect's job, with the owner watching.
func helperDaemonStarter(
	configPath func() (string, error),
	executable func() (string, error),
	environ func() []string,
	spawn func(string, []string, []string) (<-chan struct{}, error),
) func() {
	return func() {
		path, err := configPath()
		if err != nil {
			return
		}
		if _, err = daemon.ReadConfig(path); err != nil {
			return
		}
		binary, err := executable()
		if err != nil || !filepath.IsAbs(binary) {
			return
		}
		// The same detached start and environment allowlist as connect: the
		// daemon outlives this session and never sees provider credentials.
		if _, err = spawn(binary, []string{binary, "daemon", "start"}, daemonEnvironment(environ())); err != nil {
			return
		}
	}
}
