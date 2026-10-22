package daemon

import (
	"bytes"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/pelletier/go-toml/v2"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
	"github.com/temirlankasen/peerturn/internal/store"
)

const configLimit = 64 * 1024

// Config contains public local settings, never identity or session authority.
type Config struct {
	Version        int    `toml:"version"`
	StateDir       string `toml:"state_dir"`
	Relay          string `toml:"relay"`
	StorageMode    string `toml:"storage_mode"`
	PinnedRelayKey string `toml:"pinned_relay_key,omitempty"`
	AskPerDay      int    `toml:"ask_per_day"`
}

// DefaultConfigPath follows XDG, Application Support, or the Unix config directory.
func DefaultConfigPath() (string, error) {
	var dir string
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		dir = filepath.Join(base, "peerturn")
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fault.New(store.StorageUnavailable)
		}
		if runtime.GOOS == "darwin" {
			dir = filepath.Join(home, "Library", "Application Support", "PeerTurn")
		} else {
			dir = filepath.Join(home, ".config", "peerturn")
		}
	}
	canonical, err := canonicalDefault(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(canonical, "config.toml"), nil
}

// ConfiguredStateDir falls back only when the standard configuration is absent.
func ConfiguredStateDir() (string, error) {
	path, err := DefaultConfigPath()
	if err != nil {
		return "", err
	}
	config, err := ReadConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultStateDir()
	}
	if err != nil {
		return "", err
	}
	return config.StateDir, nil
}

// ReadConfig validates a bounded strict TOML file inside a pinned private root.
// A missing file or ancestor returns os.ErrNotExist without disclosing its path.
func ReadConfig(path string) (_ Config, result error) {
	if !configPath(path) {
		return Config{}, fault.New(fault.InvalidEncoding)
	}
	root, err := store.OpenPrivateDirectory(filepath.Dir(path), false)
	if err != nil {
		return Config{}, configIOError(err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil && result == nil {
			result = fault.New(store.StorageUnavailable)
		}
	}()
	return readConfig(root, filepath.Base(path))
}

// WriteConfig atomically publishes settings without replacing a different choice.
// Identical retries compare every field and do not replace the existing inode.
func WriteConfig(path, stateDir, relay, pin string) (result error) {
	config := Config{Version: 1, StateDir: stateDir, Relay: relay, StorageMode: "file_permissions", PinnedRelayKey: pin, AskPerDay: defaultAskPerDay}
	if !configPath(path) {
		return fault.New(fault.InvalidEncoding)
	}
	if err := validateConfig(config); err != nil {
		return err
	}
	root, err := store.OpenPrivateDirectory(filepath.Dir(path), true)
	if err != nil {
		return configIOError(err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil && result == nil {
			result = fault.New(store.StorageUnavailable)
		}
	}()
	data, err := toml.Marshal(config)
	if err != nil || len(data) > configLimit {
		return fault.New(fault.InvalidEncoding)
	}
	temp := ".peerturn-config-" + randomID()
	file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fault.New(store.StorageUnavailable)
	}
	defer func() {
		if removeErr := root.Remove(temp); removeErr != nil && result == nil {
			result = fault.New(store.StorageUnavailable)
		}
	}()
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return fault.New(store.StorageUnavailable)
	}
	if err = root.Link(temp, filepath.Base(path)); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fault.New(store.StorageUnavailable)
		}
		existing, readErr := readConfig(root, filepath.Base(path))
		if readErr != nil {
			return readErr
		}
		if existing != config {
			return fault.New(fault.Conflict)
		}
		return nil
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

func readConfig(root *os.Root, name string) (Config, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return Config{}, configIOError(err)
	}
	if !privateConfigFile(info) {
		return Config{}, fault.New(store.StorageUnavailable)
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return Config{}, configIOError(err)
	}
	opened, statErr := file.Stat()
	if statErr != nil || !privateConfigFile(opened) || !os.SameFile(info, opened) {
		if closeErr := file.Close(); closeErr != nil {
			return Config{}, fault.New(store.StorageUnavailable)
		}
		return Config{}, fault.New(store.StorageUnavailable)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, configLimit+1))
	if err = errors.Join(readErr, file.Close()); err != nil {
		return Config{}, fault.New(store.StorageUnavailable)
	}
	if len(data) > configLimit {
		return Config{}, fault.New(fault.TooLarge)
	}
	config := Config{AskPerDay: defaultAskPerDay}
	if err = toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&config); err != nil {
		return Config{}, fault.New(fault.InvalidEncoding)
	}
	if err = validateConfig(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func validateConfig(config Config) error {
	if config.Version != 1 || config.StorageMode != "file_permissions" || !configPath(config.StateDir) || len(config.Relay) > 4096 || config.AskPerDay <= 0 || config.AskPerDay > maxAskPerDay {
		return fault.New(fault.InvalidEncoding)
	}
	relay, err := url.Parse(config.Relay)
	if err != nil || relay.Scheme != "https" || !auth.ValidAuthority(relay.Host) || relay.User != nil || relay.Opaque != "" || relay.RawQuery != "" || relay.ForceQuery || relay.Fragment != "" || relay.RawFragment != "" || relay.Path != "" && relay.Path != "/" || relay.RawPath != "" {
		return fault.New(fault.InvalidEncoding)
	}
	if config.PinnedRelayKey != "" && !auth.ValidRoutingKey(config.PinnedRelayKey) {
		return fault.New(fault.InvalidEncoding)
	}
	root, err := store.OpenPrivateDirectory(config.StateDir, false)
	if err != nil {
		return fault.New(store.StorageUnavailable)
	}
	if err = root.Close(); err != nil {
		return fault.New(store.StorageUnavailable)
	}
	return nil
}

func configPath(path string) bool {
	return path != "" && len(path) <= 4096 && filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\x00\r\n")
}

func privateConfigFile(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(stat.Uid) == int64(os.Geteuid())
}

func configIOError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return os.ErrNotExist
	}
	return fault.New(store.StorageUnavailable)
}
