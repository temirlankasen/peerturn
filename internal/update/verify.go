package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	maxAssetSize     = 1 << 20
	maxToolOutput    = 64 << 10
	cosignTimeout    = 2 * time.Minute
	cosignIssuer     = "https://token.actions.githubusercontent.com"
	installerName    = "install.sh"
	minCosignVersion = "3.1.3"
	cosignInstall    = "install cosign ≥ " + minCosignVersion
)

var cosignVersionPattern = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

// Options describes one download-and-verify run.
type Options struct {
	Client       *http.Client
	DownloadBase string // root of <tag>/<asset> downloads, normally DownloadBase
	Release      Release
	Dir          string                       // private absolute directory owned by the caller
	LookPath     func(string) (string, error) // nil means exec.LookPath
	Environ      func() []string              // nil means the caller's environment
}

// VerifyInstaller downloads the release's install.sh, its signed checksum
// manifest and the signature bundle into opts.Dir, then returns the script
// path only if the manifest verifies against the publisher's GitHub workflow
// identity and lists the script's SHA-256. This mirrors scripts/install.sh.
func VerifyInstaller(ctx context.Context, opts Options) (string, error) {
	if !ValidVersion(opts.Release.Version) || opts.Release.Tag != "v"+opts.Release.Version {
		return "", errors.New("the release tag is not a valid version")
	}
	// A relative directory would put downloaded files into the caller's working directory.
	if !filepath.IsAbs(opts.Dir) {
		return "", errors.New("the work directory must be an absolute path")
	}
	cosign, err := requireCosign(ctx, opts)
	if err != nil {
		return "", err
	}
	manifestName := "peerturn_" + opts.Release.Version + "_checksums.txt"
	bundleName := manifestName + ".sigstore.json"
	assets := map[string][]byte{}
	for _, name := range []string{installerName, manifestName, bundleName} {
		assetURL := strings.TrimSuffix(opts.DownloadBase, "/") + "/" + opts.Release.Tag + "/" + name
		if assets[name], err = get(ctx, opts.Client, assetURL, maxAssetSize, ""); err != nil {
			return "", fmt.Errorf("could not download %s of release %s: %w; check your network and rerun peerturn upgrade", name, opts.Release.Tag, err)
		}
		if err = os.WriteFile(filepath.Join(opts.Dir, name), assets[name], 0o600); err != nil {
			return "", fmt.Errorf("could not store %s in the private work directory: %w", name, err)
		}
	}
	if err = verifyManifest(ctx, cosign, opts, filepath.Join(opts.Dir, manifestName)); err != nil {
		return "", err
	}
	manifest, err := ParseManifest(assets[manifestName])
	if err != nil {
		return "", fmt.Errorf("the signed manifest of release %s is unusable: %w; nothing was installed", opts.Release.Tag, err)
	}
	want, err := manifest.Entry(installerName)
	if err != nil {
		return "", fmt.Errorf("the signed manifest of release %s is unusable: %w; nothing was installed", opts.Release.Tag, err)
	}
	if !MatchesSHA256(assets[installerName], want) {
		return "", fmt.Errorf("install.sh of release %s does not match the signed checksum; nothing was installed", opts.Release.Tag)
	}
	return filepath.Join(opts.Dir, installerName), nil
}

// requireCosign finds cosign and enforces the minimum version before any download.
func requireCosign(ctx context.Context, opts Options) (string, error) {
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	path, err := lookPath("cosign")
	if err != nil {
		return "", errors.New("cosign is not installed; " + cosignInstall + " (brew install cosign) and rerun peerturn upgrade")
	}
	// Only stdout is JSON; cosign may print notices on stderr.
	stdout, stderr, err := runCosign(ctx, path, opts, "version", "--json")
	if err != nil {
		return "", fmt.Errorf("cosign did not report its version (%s); %s and rerun peerturn upgrade", Tail(stderr, maxTailRunes), cosignInstall)
	}
	var info struct {
		GitVersion string `json:"gitVersion"`
	}
	if json.Unmarshal(stdout, &info) != nil || !cosignRecentEnough(info.GitVersion) {
		return "", fmt.Errorf("cosign %s is too old to verify bundles safely; %s (brew upgrade cosign) and rerun peerturn upgrade", shorten(info.GitVersion), cosignInstall)
	}
	return path, nil
}

func cosignRecentEnough(gitVersion string) bool {
	match := cosignVersionPattern.FindStringSubmatch(gitVersion)
	if match == nil {
		return false
	}
	minimum := strings.Split(minCosignVersion, ".")
	for i, part := range minimum {
		have, haveErr := strconv.Atoi(match[i+1])
		need, needErr := strconv.Atoi(part)
		if haveErr != nil || needErr != nil {
			return false
		}
		if have != need {
			return have > need
		}
	}
	return true
}

// verifyManifest checks the manifest against the release workflow identity of this exact tag.
func verifyManifest(ctx context.Context, cosign string, opts Options, manifest string) error {
	identity := "https://github.com/" + Repository + "/.github/workflows/release.yml@refs/tags/" + opts.Release.Tag
	stdout, stderr, err := runCosign(ctx, cosign, opts, "verify-blob", "--bundle", manifest+".sigstore.json",
		"--certificate-identity", identity, "--certificate-oidc-issuer", cosignIssuer, manifest)
	if err != nil {
		// cosign reports verification errors on stderr.
		reason := Tail(stderr, maxTailRunes)
		if reason == "" {
			reason = Tail(stdout, maxTailRunes)
		}
		if reason == "" {
			reason = err.Error()
		}
		return fmt.Errorf("the publisher signature of release %s did not verify (%s); nothing was installed. "+
			"Retry later; if it keeps failing, do not install this release and report it to the PeerTurn maintainers", opts.Release.Tag, reason)
	}
	return nil
}

func runCosign(ctx context.Context, path string, opts Options, args ...string) (stdout, stderr []byte, err error) {
	ctx, cancel := context.WithTimeout(ctx, cosignTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, path)
	command.Args = append([]string{path}, args...)
	command.Dir = opts.Dir
	// Without an override cosign inherits the caller's HOME and proxy settings,
	// which it needs to reach its trust root.
	if opts.Environ != nil {
		command.Env = opts.Environ()
	}
	var out, errOut boundedBuffer
	command.Stdout, command.Stderr = &out, &errOut
	err = command.Run()
	return out.Bytes(), errOut.Bytes(), err
}

// boundedBuffer keeps the head of a tool's output and silently drops the rest.
type boundedBuffer struct{ buf bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := maxToolOutput - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (b *boundedBuffer) Bytes() []byte { return b.buf.Bytes() }
