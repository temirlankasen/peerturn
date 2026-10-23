package daemon

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/relayclient"
	"github.com/temirlankasen/peerturn/internal/store"
)

const journalLimit = 8 << 20

var journalAAD = []byte("peerturn/daemon/v1/client")

// DefaultStateDir follows XDG on Unix and Application Support on macOS.
func DefaultStateDir() (string, error) {
	if base := os.Getenv("XDG_STATE_HOME"); base != "" {
		return canonicalDefault(filepath.Join(base, "peerturn"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fault.New(store.StorageUnavailable)
	}
	if runtime.GOOS == "darwin" {
		return canonicalDefault(filepath.Join(home, "Library", "Application Support", "PeerTurn"))
	}
	return canonicalDefault(filepath.Join(home, ".local", "state", "peerturn"))
}

// DefaultRuntimeDir honors explicit runtime selection before the private pointer.
func DefaultRuntimeDir() (string, error) {
	if base := os.Getenv("XDG_RUNTIME_DIR"); base != "" {
		return canonicalDefault(filepath.Join(base, "peerturn", strconv.Itoa(os.Geteuid())))
	}
	if dir, found, err := pointedRuntimeDir(); err != nil || found {
		return dir, err
	}
	return daemonRuntimeDir()
}

// The publisher cannot depend on its own pointer surviving temporary-dir cleanup.
func daemonRuntimeDir() (string, error) {
	if base := os.Getenv("XDG_RUNTIME_DIR"); base != "" {
		return canonicalDefault(filepath.Join(base, "peerturn", strconv.Itoa(os.Geteuid())))
	}
	if runtime.GOOS == "darwin" {
		if base := os.Getenv("TMPDIR"); base != "" {
			return canonicalDefault(filepath.Join(base, "peerturn-"+strconv.Itoa(os.Geteuid())))
		}
	}
	return "", localipc.NewError("runtime_unavailable", "offline")
}

// Canonicalize platform ancestors, preserving the leaf for no-follow validation.
func canonicalDefault(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fault.New(fault.InvalidEncoding)
	}
	suffix := []string{filepath.Base(path)}
	path = filepath.Dir(path)
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for _, part := range slices.Backward(suffix) {
				resolved = filepath.Join(resolved, part)
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fault.New(store.StorageUnavailable)
		}
		suffix = append(suffix, filepath.Base(path))
		parent := filepath.Dir(path)
		if parent == path {
			return "", fault.New(store.StorageUnavailable)
		}
		path = parent
	}
}

// Initialize writes independent root, device, session and routing keys locally.
// Storage policy is explicit: file mode or an OS wrapping-key backend.
func Initialize(ctx context.Context, o InitOptions) (_ Status, result error) {
	o.RelayURL = strings.TrimSuffix(o.RelayURL, "/")
	if (!o.FilePermissions && o.KeyStore == nil) || !validLabel(o.SessionLabel) {
		return Status{}, localipc.NewError("unbound", "unbound")
	}
	if !filepath.IsAbs(o.StateDir) || filepath.Clean(o.StateDir) != o.StateDir {
		return Status{}, fault.New(fault.InvalidEncoding)
	}
	configPath := o.ConfigPath
	if configPath == "" {
		configPath = filepath.Join(o.StateDir, "config.toml")
	}
	if !filepath.IsAbs(configPath) || filepath.Clean(configPath) != configPath {
		return Status{}, fault.New(fault.InvalidEncoding)
	}
	var nativeSeed clientState
	if err := initializeNative(ctx, &nativeSeed, o); err != nil {
		return Status{}, err
	}
	stateLock, err := localipc.LockStateDir(o.StateDir, true)
	if err != nil {
		return Status{}, err
	}
	defer func() { result = errors.Join(result, stateLock.Close()) }()
	config, configErr := ReadConfig(configPath)
	if configErr == nil {
		if config.StateDir != o.StateDir || config.Relay != o.RelayURL || config.PinnedRelayKey != o.PinnedRelayKey {
			return Status{}, fault.New(fault.Conflict)
		}
	} else if !errors.Is(configErr, os.ErrNotExist) {
		return Status{}, configErr
	}
	routing := o.RoutingKey
	if routing == nil {
		_, routing, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return Status{}, fault.New(store.SealingFailed)
		}
	}
	if _, err = relayclient.New(relayclient.Options{URL: o.RelayURL, Key: routing, ExpectedRelayKey: o.PinnedRelayKey}); err != nil {
		return Status{}, err
	}
	if _, err = os.Lstat(filepath.Join(o.StateDir, "client.sealed")); err == nil {
		return Status{}, fault.New(fault.Conflict)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Status{}, fault.New(store.StorageUnavailable)
	}
	sealer, err := createAuthorityKey(ctx, o)
	if err != nil {
		return Status{}, err
	}
	state, err := newIdentity(o, routing)
	if err != nil {
		return Status{}, err
	}
	state.Codex = nativeSeed.Codex
	if err = saveState(o.StateDir, sealer, state, true); err != nil {
		return Status{}, err
	}
	db, err := store.Open(ctx, filepath.Join(o.StateDir, "state.db"), store.Options{Sealer: sealer})
	if err != nil {
		return Status{}, err
	}
	if err = db.InstallSession(ctx, state.Chain, identity.RootFingerprint(state.Chain.RootPublicKey), unixNow()); err != nil {
		return Status{}, errors.Join(err, db.Close())
	}
	if err = db.BindSession(ctx, mustCertificate(state).Binding, bindingContext(state)); err != nil {
		return Status{}, errors.Join(err, db.Close())
	}
	if err = db.Close(); err != nil {
		return Status{}, err
	}
	if err = WriteConfig(configPath, o.StateDir, o.RelayURL, o.PinnedRelayKey); err != nil {
		return Status{}, err
	}
	return statusFor(state, "initialized", 0, ""), nil
}

func newIdentity(o InitOptions, routing ed25519.PrivateKey) (clientState, error) {
	rootPub, root, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return clientState{}, fault.New(store.SealingFailed)
	}
	devicePub, device, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return clientState{}, fault.New(store.SealingFailed)
	}
	sessionPub, session, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return clientState{}, fault.New(store.SealingFailed)
	}
	encryption, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return clientState{}, fault.New(store.SealingFailed)
	}
	now := unixNow()
	pin := identity.RootFingerprint(rootPub)
	d := identity.DeviceCertificate{Version: 1, IdentityID: pin, DeviceID: randomID(), DeviceEpoch: 1, SigningKey: devicePub, NotBefore: now, ExpiresAt: now + identity.DeviceMaxLifetime}
	deviceCert, err := identity.IssueDevice(d, root)
	if err != nil {
		return clientState{}, err
	}
	binding := identity.Binding{IdentityID: pin, DeviceID: d.DeviceID, DeviceEpoch: 1, SessionID: randomID(), SessionEpoch: 1, KeyEpoch: 1, SigningKey: sessionPub}
	s := identity.SessionCertificate{Version: 1, Binding: binding, EncryptionKey: encryption.PublicKey().Bytes(), NotBefore: now, ExpiresAt: now + identity.SessionMaxLifetime}
	sessionCert, err := identity.IssueSession(s, device)
	if err != nil {
		return clientState{}, err
	}
	return clientState{Version: 1, RelayURL: o.RelayURL, PinnedRelayKey: o.PinnedRelayKey, Label: o.SessionLabel, RootSeed: root.Seed(), DeviceSeed: device.Seed(), SigningSeed: session.Seed(), EncryptionKey: encryption.Bytes(), RoutingSeed: routing.Seed(), Chain: identity.Chain{RootPublicKey: rootPub, Device: deviceCert, Session: sessionCert}, Peers: map[string]*peerState{}, Issued: map[string]*issuedInvite{}, Joining: map[string]*joiningInvite{}, Sequences: map[string]uint64{}, Pending: map[string]outboundOrder{}}, nil
}

func loadState(dir string, sealer store.Sealer) (clientState, error) {
	var state clientState
	root, err := os.OpenRoot(dir)
	if err != nil {
		return state, fault.New(store.StorageUnavailable)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			return
		}
	}()
	info, err := root.Lstat("client.sealed")
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return state, fault.New(store.SealingFailed)
	}
	f, err := root.Open("client.sealed")
	if err != nil {
		return state, fault.New(store.SealingFailed)
	}
	opened, statErr := f.Stat()
	if statErr != nil || !os.SameFile(info, opened) || !ownedJournal(opened) {
		return state, errors.Join(fault.New(store.SealingFailed), f.Close())
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, journalLimit+1))
	if err = errors.Join(readErr, f.Close()); err != nil || len(raw) > journalLimit {
		return state, fault.New(store.SealingFailed)
	}
	plain, err := sealer.Open(journalAAD, raw)
	if err != nil {
		return state, err
	}
	defer clear(plain)
	if err = codec.Decode(plain, journalLimit, &state); err != nil {
		return state, err
	}
	if state.Version != 1 || !validLabel(state.Label) || len(state.SigningSeed) != 32 || len(state.RoutingSeed) != 32 || len(state.EncryptionKey) != 32 || state.Peers == nil || state.Issued == nil || state.Joining == nil || state.Sequences == nil || state.Pending == nil {
		return state, fault.New(store.InvalidState)
	}
	cert, err := identity.VerifyChain(state.Chain, identity.RootFingerprint(state.Chain.RootPublicKey), unixNow())
	if err != nil {
		return state, err
	}
	key, err := ecdh.X25519().NewPrivateKey(state.EncryptionKey)
	if err != nil || !equalBytes(key.PublicKey().Bytes(), cert.EncryptionKey) || !equalBytes(ed25519.NewKeyFromSeed(state.SigningSeed)[32:], cert.SigningKey) {
		return state, fault.New(store.InvalidState)
	}
	return state, nil
}

func ownedJournal(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(stat.Uid) == int64(os.Geteuid()) && stat.Nlink == 1
}

func saveState(dir string, sealer store.Sealer, state clientState, initial bool) (result error) {
	raw, err := codec.Canonical(state)
	if err != nil {
		return err
	}
	defer clear(raw)
	if len(raw) > journalLimit {
		return fault.New(store.Backpressure)
	}
	sealed, err := sealer.Seal(journalAAD, raw)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fault.New(store.StorageUnavailable)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			result = fault.New(store.StorageUnavailable)
		}
	}()
	name := ".client-" + randomID()
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fault.New(store.StorageUnavailable)
	}
	defer func() {
		if removeErr := root.Remove(name); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			result = fault.New(store.StorageUnavailable)
		}
	}()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(sealed)
	}
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return fault.New(store.StorageUnavailable)
	}
	if initial {
		err = root.Link(name, "client.sealed")
	} else {
		err = root.Rename(name, "client.sealed")
	}
	if err != nil {
		return fault.New(store.StorageUnavailable)
	}
	directory, err := root.Open(".")
	if err != nil {
		return fault.New(store.StorageUnavailable)
	}
	if err = errors.Join(directory.Sync(), directory.Close()); err != nil {
		return fault.New(store.StorageUnavailable)
	}
	return nil
}

func randomID() string { var b [16]byte; rand.Read(b[:]); return hex.EncodeToString(b[:]) }
