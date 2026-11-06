package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/temirlankasen/peerturn/internal/update"
)

const (
	upgradeUsage      = "peerturn: upgrade accepts only --check, --verbose and --json\n"
	upgradeDevMessage = "this PeerTurn is a development build without a release version; install a release from https://github.com/temirlankasen/peerturn/releases and rerun peerturn upgrade"
	upgradeReloadNote = "Sessions that are already open keep the old plugin until /reload-plugins; new sessions use the new one."
	installedBinary   = "~/.local/bin/peerturn"
)

var upgradeHosts = []string{"claude", "codex"}

type upgradeOptions struct{ check, verbose, json bool }

type checkResult struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
}

// upgradeResult is the --json output; the unexported fields only shape the plain text.
type upgradeResult struct {
	Current   string   `json:"current"`
	Latest    string   `json:"latest"`
	Installed bool     `json:"installed"`
	Plugins   []string `json:"plugins"`
	Daemon    string   `json:"daemon"` // restarted, not_running or kept

	target   string // version the binary and daemon end up on
	verified bool   // the installer came from a freshly verified download
}

// upgradeStreams separates foreign tool output (shown only with --verbose)
// from PeerTurn's own warnings.
type upgradeStreams struct{ tools, warn io.Writer }

// upgrader holds every outside dependency so the whole flow runs against fakes in tests.
type upgrader struct {
	version      string
	client       *http.Client
	apiBase      string
	downloadBase string
	tempRoot     string // "" means the system temporary directory
	home         func() (string, error)
	lookPath     func(string) (string, error)
	runScript    func(ctx context.Context, script string, out io.Writer) error
	runBinary    func(ctx context.Context, binary string, args []string, out io.Writer) error
	daemon       connector
}

func runUpgrade(args []string, stdout, stderr io.Writer) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return runUpgradeWith(ctx, args, stdout, stderr, newUpgrader())
}

func runUpgradeWith(ctx context.Context, args []string, stdout, stderr io.Writer, u upgrader) int {
	options, ok := parseUpgradeArguments(args)
	if !ok {
		return writeText(stderr, upgradeUsage, 2)
	}
	if options.check {
		result, err := u.check(ctx)
		if err != nil {
			return writeUpgradeFailure(options, stdout, stderr, err)
		}
		return writeUpgradeOutput(options, stdout, result, result.text())
	}
	streams := upgradeStreams{tools: io.Discard, warn: stderr}
	if options.verbose {
		streams.tools = stderr
	}
	result, err := u.upgrade(ctx, streams)
	if err != nil {
		return writeUpgradeFailure(options, stdout, stderr, err)
	}
	return writeUpgradeOutput(options, stdout, result, result.text())
}

func parseUpgradeArguments(args []string) (upgradeOptions, bool) {
	var options upgradeOptions
	for _, arg := range args {
		switch arg {
		case "--check":
			options.check = true
		case "--verbose":
			options.verbose = true
		case "--json":
			options.json = true
		default:
			return upgradeOptions{}, false
		}
	}
	return options, true
}

func writeText(w io.Writer, text string, code int) int {
	if _, err := io.WriteString(w, text); err != nil {
		return 1
	}
	return code
}

func writeUpgradeOutput(options upgradeOptions, stdout io.Writer, result any, text string) int {
	if !options.json {
		return writeText(stdout, text, 0)
	}
	if json.NewEncoder(stdout).Encode(result) != nil {
		return 1
	}
	return 0
}

func writeUpgradeFailure(options upgradeOptions, stdout, stderr io.Writer, failure error) int {
	if !options.json {
		return writeText(stderr, "peerturn: upgrade failed: "+failure.Error()+"\n", 1)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	body.Error.Code, body.Error.Message = "upgrade_failed", failure.Error()
	encoder := json.NewEncoder(stdout)
	// Messages carry arrows and URLs that should stay readable, not HTML-escaped.
	encoder.SetEscapeHTML(false)
	if encoder.Encode(body) != nil {
		return 1
	}
	return 1
}

func (r checkResult) text() string {
	if r.UpdateAvailable {
		return fmt.Sprintf("PeerTurn %s; latest %s (run peerturn upgrade)\n", r.Current, r.Latest)
	}
	return fmt.Sprintf("PeerTurn %s is the latest release.\n", r.Current)
}

func (r upgradeResult) text() string {
	if !r.Installed {
		if len(r.Plugins) > 0 {
			// A rerun after an interrupted upgrade finishes the plugin setup.
			return fmt.Sprintf("PeerTurn %s is the latest release; %s.\n", r.Current, r.pluginsText())
		}
		return fmt.Sprintf("PeerTurn %s is the latest release.\n", r.Current)
	}
	subject := "PeerTurn " + r.Current
	if r.target != r.Current {
		subject += " → " + r.target
	}
	installed := "already installed"
	if r.verified {
		installed = "installed (publisher signature verified)"
	}
	daemon := map[string]string{
		"restarted":   "daemon restarted (profile, peers and links kept)",
		"not_running": "daemon not running",
		"kept":        "daemon already current",
	}[r.Daemon]
	text := fmt.Sprintf("%s: %s; %s; %s.\n", subject, installed, r.pluginsText(), daemon)
	if slices.Contains(r.Plugins, "claude") {
		text += upgradeReloadNote + "\n"
	}
	return text
}

func (r upgradeResult) pluginsText() string {
	names := make([]string, 0, len(r.Plugins))
	for _, plugin := range r.Plugins {
		names = append(names, strings.ToUpper(plugin[:1])+plugin[1:])
	}
	switch len(names) {
	case 0:
		return "plugins: none"
	case 1:
		return names[0] + " plugin updated"
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] + " plugins updated"
	}
}

// latestRelease refuses development builds: they have no release to compare with.
func (u upgrader) latestRelease(ctx context.Context) (update.Release, error) {
	if !update.ValidVersion(u.version) {
		return update.Release{}, errors.New(upgradeDevMessage)
	}
	release, err := update.Latest(ctx, u.client, u.apiBase)
	if err != nil {
		return update.Release{}, fmt.Errorf("could not check the latest release: %w; check your network and rerun peerturn upgrade", err)
	}
	return release, nil
}

func (u upgrader) check(ctx context.Context) (checkResult, error) {
	release, err := u.latestRelease(ctx)
	if err != nil {
		return checkResult{}, err
	}
	return checkResult{Current: u.version, Latest: release.Version, UpdateAvailable: update.Newer(u.version, release.Version)}, nil
}

func (u upgrader) paths() (versions, binary string, err error) {
	home, err := u.home()
	if err != nil || !filepath.IsAbs(home) {
		return "", "", errors.New("HOME is not set to an absolute path; set it and rerun peerturn upgrade")
	}
	return filepath.Join(home, ".local", "share", "peerturn"), filepath.Join(home, ".local", "bin", "peerturn"), nil
}
