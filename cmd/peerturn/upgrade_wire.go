package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/temirlankasen/peerturn/internal/update"
	"github.com/temirlankasen/peerturn/internal/version"
)

// newUpgrader wires the real release source, installer, host CLIs and daemon.
func newUpgrader() upgrader {
	return upgrader{
		version: version.Version, client: update.NewClient(), apiBase: update.APIBase, downloadBase: update.DownloadBase,
		home: os.UserHomeDir, lookPath: exec.LookPath, runScript: runInstallScript, runBinary: runInstalledBinary,
		daemon: newConnector(),
	}
}

// runInstallScript runs the verified installer with the system shell. The
// script is passed as an argument, never executed from its download location.
func runInstallScript(ctx context.Context, script string, out io.Writer) error {
	command := exec.CommandContext(ctx, "/bin/sh")
	command.Args = []string{"/bin/sh", script}
	command.Stdout, command.Stderr, command.Dir = out, out, "/"
	command.Env = installerEnvironment(os.Environ())
	return command.Run()
}

// installerEnvironment drops the release-source override: install.sh refuses it
// outside --lab, and an upgrade must always fetch from the official release.
func installerEnvironment(environ []string) []string {
	kept := make([]string, 0, len(environ))
	for _, entry := range environ {
		if !strings.HasPrefix(entry, "PEERTURN_RELEASE_BASE_URL=") {
			kept = append(kept, entry)
		}
	}
	return kept
}

// runInstalledBinary runs a PeerTurn binary non-interactively with its output sent to out.
func runInstalledBinary(ctx context.Context, binary string, args []string, out io.Writer) error {
	// The constant name keeps CommandContext from searching PATH for the binary;
	// the explicit path below is the one that runs.
	command := exec.CommandContext(ctx, "peerturn")
	command.Path, command.Err = binary, nil
	command.Args = append([]string{binary}, args...)
	command.Stdout, command.Stderr, command.Dir = out, out, "/"
	return command.Run()
}

// replaceBinary copies source over destination through a temporary file and a
// rename, as install.sh does, so a running copy is never half-written.
func replaceBinary(source, destination string) (err error) {
	if info, statErr := os.Lstat(destination); statErr == nil && info.Mode()&fs.ModeSymlink != 0 {
		return errors.New("the destination is a symbolic link")
	}
	directory := filepath.Dir(destination)
	if err = os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	data, err := readInstalledFile(source)
	if err != nil {
		return err
	}
	staged, err := os.CreateTemp(directory, ".peerturn-upgrade-*")
	if err != nil {
		return err
	}
	defer func() {
		// The staged file is gone after a successful rename; otherwise remove it.
		if removeErr := os.Remove(staged.Name()); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) && err == nil {
			err = fmt.Errorf("could not remove the staged copy: %w", removeErr)
		}
	}()
	if _, err = staged.Write(data); err != nil {
		return errors.Join(err, staged.Close())
	}
	if err = staged.Chmod(0o755); err != nil {
		return errors.Join(err, staged.Close())
	}
	if err = staged.Close(); err != nil {
		return err
	}
	return os.Rename(staged.Name(), destination)
}

// readInstalledFile reads one file through a root handle, so a swapped path
// component cannot redirect the read outside the version directory.
func readInstalledFile(path string) (data []byte, err error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	return root.ReadFile(filepath.Base(path))
}
