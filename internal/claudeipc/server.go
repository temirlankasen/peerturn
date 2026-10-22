package claudeipc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/temirlankasen/peerturn/internal/claude"
	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

// Options are local runtime facts supplied by the installed mod, never a peer.
type Options struct {
	NativeSession, RuntimeVersion, Provider string
	Client                                  localipc.Client
}

type bridge struct {
	options              Options
	instance, capability string
	shutdown             func()
	nativeGuard          func(context.Context) error
	idleMu               sync.Mutex
	idleTimer            *time.Timer
	idleTimeout          time.Duration
	lastAuthenticated    time.Time
}

// Serve publishes a single capability on its inherited private stdout pipe. The
// listener accepts only loopback bearer requests; no endpoint fetches URLs/files.
func Serve(ctx context.Context, stdout io.Writer, options Options) error {
	if options.RuntimeVersion != "2.1.306" {
		return localipc.NewError("unsupported_version", "unsupported")
	}
	nativeGuard, err := newNativeGuard(ctx)
	if err != nil {
		return err
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return rebind()
	}
	// Unqualified runtime facts close only ask. They must not remove ordinary
	// owner/native binding on macOS or unsupported provider configurations.
	var configuration json.RawMessage
	if parent, captureErr := claude.CaptureResponderConfiguration(os.Getppid(), workingDirectory, os.Environ()); captureErr == nil {
		configuration, err = json.Marshal(parent)
		if err != nil {
			return rebind()
		}
	}
	if err = nativeGuard(ctx); err != nil {
		return err
	}
	raw, err := options.Client.Call(ctx, localipc.Request{Method: "adapter.open", NativeSession: options.NativeSession, NativeSocket: os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET"), RuntimeVersion: options.RuntimeVersion, Provider: options.Provider, WorkingDirectory: workingDirectory, NativeConfiguration: configuration})
	if err != nil {
		return err
	}
	var registered struct {
		Instance string `json:"instance"`
	}
	if err = json.Unmarshal(raw, &registered); err != nil || registered.Instance == "" {
		return rebind()
	}
	var capBytes [32]byte
	rand.Read(capBytes[:])
	b := &bridge{options: options, instance: registered.Instance, capability: hex.EncodeToString(capBytes[:]), nativeGuard: nativeGuard}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return localipc.NewError("adapter_unavailable", "unavailable")
	}
	server := &http.Server{Handler: b, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 4096}
	b.shutdown = func() {
		if closeErr := server.Close(); closeErr != nil {
			return
		}
	}
	b.startIdleTimeout(30 * time.Second)
	defer b.stopIdleTimeout()
	stop := context.AfterFunc(ctx, b.shutdown)
	defer stop()
	defer b.shutdown()
	defer func() {
		if closeErr := listener.Close(); closeErr != nil {
			return
		}
	}()
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, closeErr := options.Client.Call(closeCtx, localipc.Request{Method: "adapter.close", Instance: b.instance}); closeErr != nil {
			return
		}
	}()
	if err = json.NewEncoder(stdout).Encode(map[string]string{"type": "ready", "url": "http://" + listener.Addr().String(), "capability": b.capability}); err != nil {
		return err
	}
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (b *bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Host == "" || !strings.HasPrefix(r.Host, "127.0.0.1:") || r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+b.capability)) != 1 {
		http.Error(w, `{"error":"auth_failed"}`, http.StatusForbidden)
		return
	}
	b.authenticated()
	if r.Method == http.MethodPost && r.URL.Path == "/close" {
		defer b.shutdownSoon()
	}
	request := localipc.Request{Instance: b.instance}
	switch {
	case r.URL.Path == "/poll" && r.Method == http.MethodGet:
		request.Method = "adapter.poll"
	case r.Method == http.MethodPost:
		var event struct {
			Nonce           string          `json:"nonce"`
			Session         string          `json:"session"`
			NoticeID        string          `json:"notice_id"`
			MessageID       string          `json:"message_id"`
			AttemptID       string          `json:"attempt_id"`
			Result          json.RawMessage `json:"result"`
			InvitationLinks []string        `json:"invitation_links"`
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 128*1024+1))
		if err != nil || len(data) > 128*1024 || codec.Decode(data, 128*1024, &event) != nil {
			http.Error(w, `{"error":"invalid_arguments"}`, 400)
			return
		}
		request.Nonce, request.NativeSession, request.MessageID, request.AttemptID = event.Nonce, event.Session, event.MessageID, event.AttemptID
		switch r.URL.Path {
		case "/confirm":
			request.Method = "adapter.confirm"
		case "/owner-boundary":
			if len(event.InvitationLinks) > 32 {
				http.Error(w, `{"error":"invalid_arguments"}`, 400)
				return
			}
			for _, link := range event.InvitationLinks {
				if len(link) > invite.MaxLinkBytes {
					http.Error(w, `{"error":"invalid_arguments"}`, 400)
					return
				}
				request.OwnerInvites = append(request.OwnerInvites, invite.PromptHashes(link)...)
			}
			request.Method = "adapter.boundary"
			request.NativeSocket = os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET")
		case "/boundary-attached":
			request.Method, request.Nonce = "adapter.boundary-attached", event.NoticeID
			request.NativeSocket = os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET")
		case "/authorize":
			request.Method = "adapter.authorize"
		case "/release":
			request.Method = "adapter.release"
		case "/close":
			request.Method = "adapter.close"
		case "/result":
			request.Method, request.Outcome = "adapter.result", "in_doubt"
			var result map[string]json.RawMessage
			if json.Unmarshal(event.Result, &result) == nil && result != nil {
				if _, ok := result["drop"]; ok {
					request.Outcome = "drop"
				} else {
					var native struct {
						Text   string `json:"text"`
						Origin struct {
							Kind string `json:"kind"`
							Name string `json:"name"`
						} `json:"origin"`
					}
					if json.Unmarshal(event.Result, &native) == nil && native.Origin.Kind == "plugin" && native.Origin.Name == "peerturn" {
						digest := sha256.Sum256([]byte(native.Text))
						request.Nonce = hex.EncodeToString(digest[:])
						request.Outcome = "accepted"
					}
				}
			}
		default:
			http.NotFound(w, r)
			return
		}
	default:
		http.Error(w, `{"error":"invalid_arguments"}`, http.StatusMethodNotAllowed)
		return
	}
	callCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if b.nativeGuard != nil {
		if err := b.nativeGuard(callCtx); err != nil {
			if request.Method != "adapter.close" {
				b.shutdownSoon()
			}
			http.Error(w, `{"error":"needs_rebind"}`, http.StatusConflict)
			return
		}
	}
	result, err := b.options.Client.Call(callCtx, request)
	if err != nil {
		if request.Method != "adapter.close" && localipc.PublicError(err).Code == "needs_rebind" {
			b.shutdownSoon()
		}
		http.Error(w, `{"error":"`+localipc.PublicError(err).Code+`"}`, http.StatusConflict)
		return
	}
	if _, err = w.Write(result); err != nil {
		return
	}
}

func (b *bridge) shutdownSoon() {
	if b.shutdown != nil {
		time.AfterFunc(100*time.Millisecond, b.shutdown)
	}
}

func (b *bridge) startIdleTimeout(timeout time.Duration) {
	b.idleMu.Lock()
	defer b.idleMu.Unlock()
	b.idleTimeout, b.lastAuthenticated = timeout, time.Now()
	b.idleTimer = time.AfterFunc(timeout, b.expireIdle)
}

func (b *bridge) authenticated() {
	b.idleMu.Lock()
	defer b.idleMu.Unlock()
	if b.idleTimer != nil {
		b.lastAuthenticated = time.Now()
		b.idleTimer.Reset(b.idleTimeout)
	}
}

func (b *bridge) expireIdle() {
	b.idleMu.Lock()
	defer b.idleMu.Unlock()
	if b.idleTimer == nil {
		return
	}
	if remaining := b.idleTimeout - time.Since(b.lastAuthenticated); remaining > 0 {
		b.idleTimer.Reset(remaining)
		return
	}
	b.idleTimer = nil
	if b.shutdown != nil {
		b.shutdown()
	}
}

func (b *bridge) stopIdleTimeout() {
	b.idleMu.Lock()
	defer b.idleMu.Unlock()
	if b.idleTimer != nil {
		b.idleTimer.Stop()
		b.idleTimer = nil
	}
}
