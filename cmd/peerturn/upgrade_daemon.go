package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// stopPollAttempts at connectPollInterval gives the old daemon 10 seconds to go away.
const stopPollAttempts = 50

// daemonVersion asks the running daemon which release it executes.
func (u upgrader) daemonVersion(ctx context.Context) (running bool, version string, err error) {
	status, err := u.daemon.status(ctx)
	switch {
	case err == nil:
		return true, status.PeerturnVersion, nil
	case errorCode(err) == "unavailable":
		return false, "", nil
	default:
		return false, "", fmt.Errorf("could not query the running daemon (%s); run peerturn daemon status, fix what it reports and rerun peerturn upgrade", errorCode(err))
	}
}

// restartDaemon replaces a daemon that still runs older code. A daemon that is
// not running stays stopped: sessions start it with peerturn connect. The
// profile, peers and links live in the state directory, which is never touched.
func (u upgrader) restartDaemon(ctx context.Context, binary, target string) (string, error) {
	conn := u.daemon
	conn.executable = func() (string, error) { return binary, nil }
	running, version, err := u.daemonVersion(ctx)
	if err != nil {
		return "", err
	}
	if !running {
		return "not_running", nil
	}
	if version == target {
		return "kept", nil
	}
	if _, err = conn.call(ctx, localipc.Request{Method: "stop"}); err != nil && errorCode(err) != "unavailable" {
		return "", fmt.Errorf("could not ask the old daemon to stop (%s); run peerturn daemon stop and rerun peerturn upgrade", errorCode(err))
	}
	if err = u.waitUntilStopped(ctx, conn); err != nil {
		return "", err
	}
	if err = conn.startDaemon(ctx); err != nil {
		return "", fmt.Errorf("the new daemon did not start: %s; the old one is stopped, so run peerturn daemon start in a terminal to see why", daemonProblem(err))
	}
	if running, version, err = u.daemonVersion(ctx); err != nil || !running || version != target {
		return "", fmt.Errorf("the daemon answers, but not as PeerTurn %s; run peerturn daemon stop, then peerturn daemon status, and rerun peerturn upgrade", target)
	}
	return "restarted", nil
}

func (u upgrader) waitUntilStopped(ctx context.Context, conn connector) error {
	for range stopPollAttempts {
		_, err := conn.status(ctx)
		if err != nil {
			if errorCode(err) == "unavailable" {
				return nil
			}
			return fmt.Errorf("could not confirm that the old daemon stopped (%s); run peerturn daemon status and rerun peerturn upgrade", errorCode(err))
		}
		if err = conn.wait(ctx, connectPollInterval); err != nil {
			return errors.New("interrupted while waiting for the old daemon to stop; check peerturn daemon status before rerunning peerturn upgrade")
		}
	}
	return errors.New("the old daemon did not stop within 10 seconds; run peerturn daemon stop, check peerturn daemon status, then rerun peerturn upgrade")
}

// daemonProblem prefers the connect helper's plain-English explanation.
func daemonProblem(err error) string {
	public := localipc.PublicError(err)
	if public.Message != "" {
		return public.Message
	}
	return public.Code
}
