package daemon

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"github.com/temirlankasen/peerturn/internal/claude"
	"github.com/temirlankasen/peerturn/internal/claudeipc"
	"maps"
	"path/filepath"
	"sync"
	"time"

	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/keystore"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/relayclient"
	"github.com/temirlankasen/peerturn/internal/store"
)

// Daemon serializes owner operations with custody and the local onboarding journal.
type Daemon struct {
	mu                       sync.Mutex
	opMu                     sync.Mutex
	options                  Options
	state                    clientState
	sealer                   store.Sealer
	db                       *store.Store
	relay                    *relayclient.Client
	key                      *ecdh.PrivateKey
	cert                     identity.SessionCertificate
	capabilities             map[string]capabilityBinding
	joinOrigins              joinOrigins
	stateLock                *localipc.StateLock
	closeOnce                sync.Once
	closeErr                 error
	wake                     chan struct{}
	cancel                   context.CancelFunc
	verified                 int64
	lastError                string
	persistenceFailed        bool
	lastMaintenance          int64
	ackedThrough             int64
	notifyOnline             bool
	claudeBinding            *claude.Binding
	claudeCurrentProof       *claudeipc.NativeProcessProof
	claudeAttempt            *adapterEvent
	claudeNotice             *informNotice
	native                   NativeRuntime
	hookVerified             int64
	informOffer              *informOffer
	responderPreflight       ResponderPreflight
	askActive                map[string]context.CancelFunc
	askWorkers               sync.WaitGroup
	askContext               context.Context
	askCancel                context.CancelFunc
	authoritySecretsIsolated bool
	authoritySecretsReason   string
	authorityKeyStore        keystore.Backend
	authorityKeyID           string
	askCleanupFailed         bool
}

// Open validates existing key continuity; it never replaces missing key material.
func Open(ctx context.Context, o Options) (_ *Daemon, result error) {
	stateLock, err := localipc.LockStateDir(o.StateDir, false)
	if err != nil {
		return nil, err
	}
	defer func() {
		if result != nil {
			result = errors.Join(result, stateLock.Close())
		}
	}()
	authority, err := openAuthorityKey(ctx, o.StateDir, o.KeyStore)
	if err != nil {
		return nil, err
	}
	sealer := authority.sealer
	state, err := loadState(o.StateDir, sealer)
	if err != nil {
		return nil, err
	}
	if err = validatePeerState(state); err != nil {
		return nil, err
	}
	client, err := relayclient.New(relayclient.Options{URL: state.RelayURL, Key: ed25519.NewKeyFromSeed(state.RoutingSeed), HTTPClient: o.HTTPClient, ExpectedRelayKey: state.PinnedRelayKey})
	if err != nil {
		return nil, err
	}
	db, err := store.Open(ctx, filepath.Join(o.StateDir, "state.db"), store.Options{Sealer: sealer, RequireExistingSchema: true})
	if err != nil {
		return nil, err
	}
	key, err := ecdh.X25519().NewPrivateKey(state.EncryptionKey)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	cert, err := identity.VerifyChain(state.Chain, identity.RootFingerprint(state.Chain.RootPublicKey), unixNow())
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err = db.InstallSession(ctx, state.Chain, cert.IdentityID, unixNow()); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err = db.BindSession(ctx, cert.Binding, bindingContext(state)); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	d := &Daemon{options: o, state: state, sealer: sealer, db: db, relay: client, key: key, cert: cert, capabilities: map[string]capabilityBinding{}, stateLock: stateLock, wake: make(chan struct{}, 1)}
	d.native = o.Native
	d.askContext, d.askCancel = context.WithCancel(context.Background())
	d.askActive = map[string]context.CancelFunc{}
	d.authoritySecretsIsolated = authority.isolated
	d.authoritySecretsReason = authority.reason
	d.authorityKeyStore = authority.backend
	d.authorityKeyID = authority.id
	if err = d.configureAskBudget(); err != nil {
		return nil, errors.Join(err, d.Close())
	}
	d.responderPreflight = d.checkResponderStartup()
	if err = db.RecoverAskRuns(ctx, unixNow()); err != nil {
		return nil, errors.Join(err, d.Close())
	}
	if err = d.recoverAskBudget(ctx); err != nil {
		return nil, errors.Join(err, d.Close())
	}
	d.askCleanupFailed = d.recoverAskArtifacts(ctx) != nil
	if err = db.RecoverInDoubt(ctx, unixNow()); err != nil {
		return nil, errors.Join(err, d.Close())
	}
	if err = d.refreshPeerRevocations(ctx); err != nil {
		return nil, errors.Join(err, d.Close())
	}
	return d, nil
}

// Close releases custody only after the caller has stopped sync/IPC workers.
func (d *Daemon) Close() error {
	d.closeOnce.Do(func() {
		d.mu.Lock()
		d.wipeOwnerInvites()
		d.mu.Unlock()
		if d.askCancel != nil {
			d.askCancel()
		}
		d.askWorkers.Wait()
		if closer, ok := d.native.(interface{ Close() error }); ok {
			d.closeErr = closer.Close()
		}
		d.closeErr = errors.Join(d.closeErr, d.db.Close(), d.stateLock.Close())
	})
	return d.closeErr
}

// Run owns the one-uid IPC lock for its entire lifetime, including shutdown.
func Run(ctx context.Context, o Options) (result error) {
	if o.RuntimeDir == "" {
		var err error
		o.RuntimeDir, err = daemonRuntimeDir()
		if err != nil {
			return err
		}
	}
	server, err := localipc.Listen(o.RuntimeDir)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, server.Close())
	}()
	d, err := Open(ctx, o)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.Close())
	}()
	pointer, err := publishRuntimePointer(o.StateDir, o.RuntimeDir)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, pointer.Close())
	}()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	d.cancel = cancel
	done := make(chan struct{})
	go func() { defer close(done); d.loop(runCtx) }()
	err = server.Serve(runCtx, d.Dispatch)
	cancel()
	<-done
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (d *Daemon) loop(ctx context.Context) {
	notifyDone := make(chan struct{})
	go func() {
		defer close(notifyDone)
		d.notifyLoop(ctx)
	}()
	defer func() { <-notifyDone }()
	timer := time.NewTimer(0)
	defer timer.Stop()
	backoff := time.Second
	var retryAt time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.wake:
			if time.Now().Before(retryAt) {
				continue
			}
		case <-timer.C:
		}
		if ctx.Err() != nil {
			return
		}
		callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := d.Sync(callCtx)
		cancel()
		d.mu.Lock()
		if err != nil {
			d.lastError = safeCode(err)
		} else {
			d.verified = unixNow()
			d.lastError = ""
		}
		pending := false
		for _, i := range d.state.Issued {
			if !i.Sent && !i.Terminal {
				pending = true
			}
		}
		for _, j := range d.state.Joining {
			if !j.AckSent && !j.Terminal {
				pending = true
			}
		}
		d.mu.Unlock()
		interval := 10 * time.Second
		if err != nil {
			interval = backoff
			backoff = min(30*time.Second, backoff*2)
			retryAt = time.Now().Add(interval)
		} else {
			backoff = time.Second
			retryAt = time.Time{}
			if pending {
				interval = 2 * time.Second
			}
		}
		timer.Reset(interval)
	}
}

func (d *Daemon) signal() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}
func (d *Daemon) save() error {
	if r := d.state.Reserved; r != nil {
		if r.RevokeGrant != "" {
			maps.Copy(d.state.Pending, r.Orders)
		} else if len(r.NativeBatch) == 0 {
			d.state.Pending[r.Packet.Header.MessageID] = outboundOrder{PeerID: r.PeerID, SenderSeq: r.Message.SenderSeq}
		}
	}
	err := saveState(d.options.StateDir, d.sealer, d.state, false)
	if err != nil {
		d.persistenceFailed = true
	}
	return err
}
func (d *Daemon) mailbox() string { return peercrypto.Digest(d.routingPublic()) }
func (d *Daemon) routingPublic() []byte {
	return ed25519.NewKeyFromSeed(d.state.RoutingSeed)[ed25519.SeedSize:]
}
func (d *Daemon) signingKey() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(d.state.SigningSeed) }
func publicKey(key []byte) string                { return base64.RawURLEncoding.EncodeToString(key) }
func routeMailbox(key []byte) string             { return peercrypto.Digest(key) }
func routePermit(key []byte) string              { return peercrypto.Digest(key) }
func unixNow() int64                             { return time.Now().Unix() }
func equalBytes(a, b []byte) bool                { return bytes.Equal(a, b) }
func validLabel(s string) bool {
	return len(s) > 0 && len(s) <= 128 && bytes.IndexFunc([]byte(s), func(r rune) bool { return r < 32 || r == 127 }) < 0
}
func mustCertificate(s clientState) identity.SessionCertificate {
	cert, err := identity.VerifyChain(s.Chain, identity.RootFingerprint(s.Chain.RootPublicKey), unixNow())
	if err != nil {
		panic("invalid newly issued identity")
	}
	return cert
}

func statusFor(s clientState, state string, verified int64, lastError string) Status {
	status := Status{State: state, Binding: bindingLabel(s), Session: s.Label, Fingerprint: identity.RootFingerprint(s.Chain.RootPublicKey), RoutingKey: publicKey(ed25519.NewKeyFromSeed(s.RoutingSeed)[32:]), SupportedScopes: []string{"coauthor"}, LastVerifiedAt: verified, LastError: lastError,
		Capabilities: Capabilities{E2E: true, ReceiptStages: []string{"sender_stored", "relay_stored", "receiver_stored"}},
		Warning:      "Explicit test session; native session binding and adapters arrive in M3/M4. Coauthor is strong trust with the receiving agent's normal capabilities, not a sandbox. Peer text can influence later owner turns. URL fragments appear in pasted transcripts."}
	if s.Codex != nil {
		status.NativeThread = s.Codex.ThreadID
		status.Warning = "Codex shared-daemon binding; run status to probe active capabilities. Coauthor is strong trust with normal receiving-agent capabilities, not a sandbox. Peer text can influence later owner turns."
	}
	for _, i := range s.Issued {
		if i.Failure != "" {
			status.OnboardingErrors = append(status.OnboardingErrors, i.Failure)
		}
	}
	for _, j := range s.Joining {
		if j.Failure != "" {
			status.OnboardingErrors = append(status.OnboardingErrors, j.Failure)
		}
	}
	return status
}

func safeCode(err error) string {
	if e, ok := errors.AsType[*fault.Error](err); ok {
		return string(e.Code)
	}
	if e, ok := errors.AsType[*localipc.Error](err); ok {
		return e.Code
	}
	if e, ok := errors.AsType[*relayclient.Error](err); ok {
		return e.Code
	}
	return "transport_unavailable"
}
