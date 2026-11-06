package daemon

import (
	"io"
	"log/slog"
	"path/filepath"

	"github.com/temirlankasen/peerturn/internal/logfile"
)

// The operator log says what happened by code, identifier and stage, so a
// stalled link or delivery can be explained without guessing. It carries no
// bodies, links, grants, nonces, tokens or keys: those never leave custody.
const (
	logFileLimit = 4 << 20
	logFileKeep  = 3
)

// LogPath is where the daemon of a state directory writes its operator log.
func LogPath(stateDir string) string {
	return filepath.Join(stateDir, "logs", "daemon.log")
}

// openLog never fails the daemon: an unwritable log directory yields a silent
// logger, because custody and delivery matter more than the diagnostic trail.
func openLog(stateDir string) (*slog.Logger, io.Closer) {
	w, err := logfile.Open(LogPath(stateDir), logFileLimit, logFileKeep)
	if err != nil {
		return slog.New(slog.DiscardHandler), io.NopCloser(nil)
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})), w
}

func (d *Daemon) logger() *slog.Logger {
	if d.log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return d.log
}

// shortID keeps identifiers recognisable in the log without filling it.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// logged names the owner commands whose failure an operator wants to see.
func logged(method string) bool {
	switch method {
	case "invite", "join", "join.owner", "join.text", "send", "reply", "revoke", "session.unbind", "delivery.resolve", "invite.revoke":
		return true
	}
	return false
}
