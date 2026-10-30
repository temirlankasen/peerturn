package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// The daemon outlives the native session; retain relay connection settings,
// never provider or native-session credentials.
var daemonEnvironmentNames = map[string]bool{
	"HOME": true, "USER": true, "LOGNAME": true, "PATH": true, "TMPDIR": true, "LANG": true, "TZ": true,
	"XDG_RUNTIME_DIR": true, "XDG_CONFIG_HOME": true, "XDG_STATE_HOME": true, "XDG_DATA_HOME": true, "XDG_CACHE_HOME": true,
	"HTTPS_PROXY": true, "https_proxy": true, "HTTP_PROXY": true, "http_proxy": true, "ALL_PROXY": true, "all_proxy": true,
	"NO_PROXY": true, "no_proxy": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
}

func daemonEnvironment(environ []string) []string {
	seen := map[string]bool{}
	// A nil exec.Cmd.Env would inherit the caller's full environment.
	kept := []string{}
	for _, entry := range environ {
		name, value, found := strings.Cut(entry, "=")
		if !found || seen[name] || strings.ContainsAny(value, "\r\n\x00") {
			continue
		}
		if daemonEnvironmentNames[name] || (strings.HasPrefix(name, "LC_") && len(name) > len("LC_") && !strings.ContainsAny(name, " \t")) {
			seen[name] = true
			kept = append(kept, entry)
		}
	}
	return kept
}

func (c connector) startDaemon(ctx context.Context) error {
	executable, err := c.executable()
	if err != nil || !filepath.IsAbs(executable) {
		return localipc.NewError("unavailable", "unavailable")
	}
	exited, err := c.spawn(executable, []string{executable, "daemon", "start"}, daemonEnvironment(c.environ()))
	if err != nil {
		return localipc.NewError("unavailable", "unavailable")
	}
	for range connectReadyAttempts {
		_, err = c.status(ctx)
		if err == nil {
			return nil
		}
		if errorCode(err) != "unavailable" {
			return err
		}
		select {
		case <-exited:
			// Another connect may have acquired the daemon lock before this child exited.
			if _, err = c.status(ctx); err == nil {
				return nil
			}
			return connectError("unavailable", "unavailable", "The daemon exited before it became ready. Run peerturn daemon start in a terminal to see its failure.")
		default:
		}
		if err = c.wait(ctx, connectPollInterval); err != nil {
			return localipc.PublicError(err)
		}
	}
	return connectError("timeout", "pending", "The daemon did not become ready in time. It was left running; check peerturn daemon status before retrying.")
}

// Detach from native tool cleanup and avoid inherited streams or diagnostic logs.
func spawnDetached(executable string, argv, env []string) (<-chan struct{}, error) {
	command := exec.Command(executable)
	command.Args, command.Env, command.Dir = argv, append([]string{}, env...), "/"
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return nil, err
	}
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		if err := command.Wait(); err != nil {
			return
		}
	}()
	return exited, nil
}

func sleepContext(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
