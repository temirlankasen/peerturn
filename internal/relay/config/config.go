// Package config validates the explicitly supplied relay TOML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
)

const maxConfigBytes = 64 * 1024

// Config contains operator-controlled paths, listeners, authentication and limits.
type Config struct {
	Listen          string   `toml:"listen"`
	PrivateListen   string   `toml:"private_listen"`
	DataDir         string   `toml:"data_dir"`
	TLSCert         string   `toml:"tls_cert"`
	TLSKey          string   `toml:"tls_key"`
	PublicAuthority string   `toml:"public_authority"`
	ClientIPHeader  string   `toml:"client_ip_header"`
	TrustedProxies  []string `toml:"trusted_proxies"`
	AdminKeys       []string `toml:"admin_keys"`
	BootstrapFile   string   `toml:"bootstrap_file"`
	Limits          Limits   `toml:"limits"`
}

// Limits bounds durable storage and request/notification resources.
type Limits struct {
	TotalBytes            int64 `toml:"total_bytes"`
	MailboxBytes          int64 `toml:"mailbox_bytes"`
	MailboxItems          int   `toml:"mailbox_items"`
	MetadataItems         int   `toml:"metadata_items"`
	BodyBytes             int64 `toml:"body_bytes"`
	CiphertextBytes       int   `toml:"ciphertext_bytes"`
	BatchItems            int   `toml:"batch_items"`
	LongPollSeconds       int   `toml:"long_poll_seconds"`
	RequestTimeoutSeconds int   `toml:"request_timeout_seconds"`
	ReadHeaderSeconds     int   `toml:"read_header_seconds"`
	BodyReadSeconds       int   `toml:"body_read_seconds"`
	IdleSeconds           int   `toml:"idle_seconds"`
	MaxConnections        int   `toml:"max_connections"`
	RequestsPerIP         int   `toml:"requests_per_ip"`
	IPv6PrefixBits        int   `toml:"ipv6_prefix_bits"`
	PollsPerKey           int   `toml:"polls_per_key"`
	PollsPerMailbox       int   `toml:"polls_per_mailbox"`
	MaxStreams            int   `toml:"max_streams"`
	StreamsPerKey         int   `toml:"streams_per_key"`
	StreamsPerMailbox     int   `toml:"streams_per_mailbox"`
	RatePerMinute         int   `toml:"rate_per_minute"`
	RateBurst             int   `toml:"rate_burst"`
}

// Defaults supplies bounded defaults; operator paths and trust must be explicit.
func Defaults() Config {
	return Config{
		Listen: "127.0.0.1:8443", PrivateListen: "127.0.0.1:9090",
		ClientIPHeader: "X-Forwarded-For",
		Limits: Limits{
			TotalBytes: 1 << 30, MailboxBytes: 32 << 20, MailboxItems: 1000,
			MetadataItems: 100000, BodyBytes: 135168, CiphertextBytes: 96 << 10,
			BatchItems: 100, LongPollSeconds: 25, RequestTimeoutSeconds: 35,
			ReadHeaderSeconds: 5, BodyReadSeconds: 5, IdleSeconds: 60, MaxStreams: 256,
			// Ten daemons sharing a NAT need notification, fetch and spare sockets.
			MaxConnections: 512, RequestsPerIP: 32, IPv6PrefixBits: 64,
			PollsPerKey: 4, PollsPerMailbox: 4,
			StreamsPerKey: 4, StreamsPerMailbox: 4, RatePerMinute: 600, RateBurst: 120,
		},
	}
}

// Load reads only the explicitly selected local configuration file.
func Load(path string) (Config, error) {
	if path == "" || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
		return Config{}, errors.New("configuration path must be nonempty and bounded")
	}
	abs, err := filepath.Abs(path)
	if err != nil || !validPath(abs) {
		return Config{}, errors.New("cannot resolve configuration path")
	}
	f, err := os.Open(filepath.Clean(abs))
	if err != nil {
		return Config{}, fmt.Errorf("open relay configuration: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	closeErr := f.Close()
	if readErr != nil {
		return Config{}, fmt.Errorf("read relay configuration: %w", readErr)
	}
	if closeErr != nil {
		return Config{}, fmt.Errorf("close relay configuration: %w", closeErr)
	}
	return Parse(data)
}

// Parse applies defaults, rejects unknown TOML fields and validates every bound.
func Parse(data []byte) (Config, error) {
	if len(data) > maxConfigBytes {
		return Config{}, errors.New("relay configuration exceeds 64 KiB")
	}
	cfg := Defaults()
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("invalid relay TOML: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate rejects unsafe exposure and resource settings before startup effects.
func (c Config) Validate() error {
	if !auth.ValidAuthority(c.PublicAuthority) {
		return errors.New("public_authority must be an explicit canonical HTTPS authority")
	}
	if c.ClientIPHeader != "X-Forwarded-For" && c.ClientIPHeader != "X-Real-IP" {
		return errors.New("client_ip_header must be X-Forwarded-For or X-Real-IP")
	}
	if _, err := listenAddress(c.Listen, false); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if _, err := listenAddress(c.PrivateListen, true); err != nil {
		return fmt.Errorf("private_listen: %w", err)
	}
	if c.Listen == c.PrivateListen {
		return errors.New("public and private listeners must differ")
	}
	if !validPath(c.DataDir) || (c.BootstrapFile != "" && !validPath(c.BootstrapFile)) {
		return errors.New("data_dir and configured bootstrap_file must be absolute bounded paths")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return errors.New("tls_cert and tls_key must be configured together")
	}
	if c.TLSCert != "" && (!validPath(c.TLSCert) || !validPath(c.TLSKey)) {
		return errors.New("TLS certificate and key paths must be absolute and bounded")
	}
	if c.TLSCert == "" && len(c.TrustedProxies) == 0 {
		return errors.New("TLS termination or an explicit trusted proxy allowlist is required")
	}
	if len(c.TrustedProxies) > 32 {
		return errors.New("trusted proxy allowlist exceeds 32 entries")
	}
	seenProxies := make(map[netip.Prefix]bool)
	for _, raw := range c.TrustedProxies {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || !auth.ValidTrustedProxyPrefix(prefix) || seenProxies[prefix] {
			return errors.New("trusted proxy entries must be distinct canonical private or loopback CIDRs")
		}
		seenProxies[prefix] = true
	}
	if len(c.AdminKeys) == 0 || len(c.AdminKeys) > 32 {
		return errors.New("admin_keys must contain 1 to 32 routing public keys")
	}
	seenKeys := make(map[string]bool)
	for _, key := range c.AdminKeys {
		if !auth.ValidRoutingKey(key) || seenKeys[key] {
			return errors.New("admin_keys must be distinct canonical Ed25519 routing public keys")
		}
		seenKeys[key] = true
	}
	return c.Limits.validate()
}

// TrustedProxyPrefixes returns parsed prefixes after successful validation.
func (c Config) TrustedProxyPrefixes() []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(c.TrustedProxies))
	for _, raw := range c.TrustedProxies {
		if prefix, err := netip.ParsePrefix(raw); err == nil {
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes
}

// RequestTimeout is the validated HTTP request deadline.
func (c Config) RequestTimeout() time.Duration {
	return time.Duration(c.Limits.RequestTimeoutSeconds) * time.Second
}

func validPath(path string) bool {
	return path != "" && len(path) <= 4096 && !strings.ContainsAny(path, "\x00\r\n") && filepath.IsAbs(path)
}

func listenAddress(address string, private bool) (netip.Addr, error) {
	if len(address) > 256 {
		return netip.Addr{}, errors.New("listener address exceeds 256 bytes")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return netip.Addr{}, errors.New("listener must be an IP address and port")
	}
	ip, err := netip.ParseAddr(host)
	number, portErr := strconv.Atoi(port)
	if err != nil || ip.Zone() != "" || portErr != nil || number < 1 || number > 65535 {
		return netip.Addr{}, errors.New("listener must use a literal IP and port 1..65535")
	}
	if private && !ip.Unmap().IsLoopback() && !ip.Unmap().IsPrivate() {
		return netip.Addr{}, errors.New("private listener must bind loopback or a private IP")
	}
	return ip, nil
}
