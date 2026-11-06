package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/temirlankasen/peerturn/internal/update"
)

const (
	versionProbeTimeout = 10 * time.Second
	installerTimeout    = 10 * time.Minute
	setupTimeout        = 6 * time.Minute
	installerPrefix     = "peerturn installer: "
	maxCapturedOutput   = 16 << 10
)

// upgrade runs the whole chain: verify, install, plugins, daemon. Each step
// leaves a state that rerunning peerturn upgrade can continue from.
func (u upgrader) upgrade(ctx context.Context, streams upgradeStreams) (upgradeResult, error) {
	release, err := u.latestRelease(ctx)
	if err != nil {
		return upgradeResult{}, err
	}
	versions, binary, err := u.paths()
	if err != nil {
		return upgradeResult{}, err
	}
	result := upgradeResult{Current: u.version, Latest: release.Version, Plugins: []string{}, target: release.Version}
	newer := update.Newer(u.version, release.Version)
	running := false
	if !newer {
		// Never downgrade. A daemon left on another version (an interrupted upgrade,
		// or install.sh run by hand) is still finished by restarting it.
		result.target = u.version
		var version string
		var statusErr error
		running, version, statusErr = u.daemonVersion(ctx)
		if statusErr != nil {
			return upgradeResult{}, statusErr
		}
		if !running || version == u.version {
			// The plugins may still be the previous release's when an earlier
			// run stopped after installing; setup is idempotent, so finish it,
			// but only with the standard binary of this very version.
			if u.reports(ctx, binary, result.target) {
				if result.Plugins, err = u.setupHosts(ctx, binary, result.target, streams); err != nil {
					return upgradeResult{}, err
				}
			}
			result.Daemon = daemonLabel(running)
			return result, nil
		}
	}
	present, verified, err := u.ensureInstalled(ctx, release, filepath.Join(versions, result.target), binary, newer, streams)
	if err != nil {
		return upgradeResult{}, err
	}
	if !present {
		// A running release build outside the standard location has nothing to finish.
		result.Daemon = daemonLabel(running)
		return result, nil
	}
	result.Installed, result.verified = true, verified
	if result.Plugins, err = u.setupHosts(ctx, binary, result.target, streams); err != nil {
		return upgradeResult{}, err
	}
	if result.Daemon, err = u.restartDaemon(ctx, binary, result.target); err != nil {
		return upgradeResult{}, err
	}
	return result, nil
}

func daemonLabel(running bool) string {
	if running {
		return "kept"
	}
	return "not_running"
}

// ensureInstalled makes ~/.local/bin/peerturn report the target version.
// present is false only when nothing is installed and installing is not wanted.
func (u upgrader) ensureInstalled(ctx context.Context, release update.Release, directory, binary string, mayInstall bool, streams upgradeStreams) (present, verified bool, err error) {
	target := filepath.Base(directory)
	exists, err := directoryExists(directory)
	if err != nil {
		return false, false, err
	}
	switch {
	case !exists && !mayInstall:
		return false, false, nil
	case !exists:
		if err = u.verifyAndInstall(ctx, release, streams); err != nil {
			return false, false, err
		}
		verified = true
	case !u.reports(ctx, filepath.Join(directory, "peerturn"), target):
		return false, false, fmt.Errorf("%s exists but its peerturn does not report version %s; remove it and rerun peerturn upgrade", directory, target)
	}
	if !u.reports(ctx, binary, target) {
		// install.sh refuses to reinstall an unpacked version, so link it in ourselves.
		if err = replaceBinary(filepath.Join(directory, "peerturn"), binary); err != nil {
			return false, false, fmt.Errorf("could not put PeerTurn %s into %s: %w; fix that and rerun peerturn upgrade", target, installedBinary, err)
		}
		if !u.reports(ctx, binary, target) {
			return false, false, fmt.Errorf("%s does not report version %s after installing; check that it is not shadowed or read-only, then rerun peerturn upgrade", installedBinary, target)
		}
	}
	return true, verified, nil
}

func directoryExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("cannot inspect %s: %w; fix that and rerun peerturn upgrade", path, err)
	case !info.IsDir():
		// install.sh refuses links in the install path; so do we.
		return false, fmt.Errorf("%s is not a plain directory; remove it and rerun peerturn upgrade", path)
	}
	return true, nil
}

// verifyAndInstall downloads the release installer, proves it is the signed one, and only then runs it.
func (u upgrader) verifyAndInstall(ctx context.Context, release update.Release, streams upgradeStreams) (err error) {
	work, err := os.MkdirTemp(u.tempRoot, "peerturn-upgrade-")
	if err != nil {
		return fmt.Errorf("could not create a private work directory: %w; check the temporary directory and rerun peerturn upgrade", err)
	}
	defer func() {
		// Leftovers are public release files; report them without failing a finished upgrade.
		if removeErr := os.RemoveAll(work); removeErr != nil {
			if _, writeErr := fmt.Fprintf(streams.warn, "peerturn: warning: could not remove %s: %v\n", work, removeErr); writeErr != nil {
				return
			}
		}
	}()
	script, err := update.VerifyInstaller(ctx, update.Options{
		Client: u.client, DownloadBase: u.downloadBase, Release: release, Dir: work, LookPath: u.lookPath,
	})
	if err != nil {
		return err
	}
	var captured limitedBuffer
	runCtx, cancel := context.WithTimeout(ctx, installerTimeout)
	defer cancel()
	if err = u.runScript(runCtx, script, io.MultiWriter(&captured, streams.tools)); err != nil {
		return fmt.Errorf("the installer stopped with: %s; rerun peerturn upgrade --verbose for its full output", installerReason(captured.Bytes(), err))
	}
	return nil
}

// installerReason prefers the installer's own last message over a bare exit status.
func installerReason(output []byte, runErr error) string {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range slices.Backward(lines) {
		if reason, found := strings.CutPrefix(line, installerPrefix); found {
			return update.Tail([]byte(reason), 200)
		}
	}
	return runErr.Error()
}

// reports runs "<binary> version" and checks it names the wanted release.
func (u upgrader) reports(ctx context.Context, binary, version string) bool {
	ctx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	var output bytes.Buffer
	if err := u.runBinary(ctx, binary, []string{"version"}, &output); err != nil {
		return false
	}
	return strings.HasPrefix(output.String(), "peerturn "+version+" ")
}

// setupHosts refreshes the plugin of every agent CLI found on PATH, using the new binary.
func (u upgrader) setupHosts(ctx context.Context, binary, target string, streams upgradeStreams) ([]string, error) {
	updated := []string{}
	for _, host := range upgradeHosts {
		if _, err := u.lookPath(host); err != nil {
			continue
		}
		hostCtx, cancel := context.WithTimeout(ctx, setupTimeout)
		err := u.runBinary(hostCtx, binary, []string{"setup", host}, streams.tools)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("the %s plugin update failed after installing PeerTurn %s; run %s setup %s to see why, then rerun peerturn upgrade",
				strings.ToUpper(host[:1])+host[1:], target, installedBinary, host)
		}
		updated = append(updated, host)
	}
	return updated, nil
}

// limitedBuffer keeps the head of a tool's output; the installer's messages are short.
type limitedBuffer struct{ buf bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := maxCapturedOutput - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }
