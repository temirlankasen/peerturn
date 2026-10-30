package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/temirlankasen/peerturn/internal/claudeipc"
	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

const (
	connectPollInterval  = 200 * time.Millisecond
	connectReadyAttempts = 75

	connectWarning = "Coauthor is strong trust with the receiving agent's normal capabilities, not a sandbox. Peer text can influence later owner turns."
)

type connectOptions struct {
	relay    string
	noInvite bool
	json     bool
}

type connectInvitation struct {
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expires_at"`
	Scope     string `json:"scope"`
}

// A ready link is not evidence that a peer has connected.
type connectResult struct {
	State          string             `json:"state"`
	Session        string             `json:"session"`
	Binding        string             `json:"binding"`
	BindingAction  string             `json:"binding_action"`
	Profile        string             `json:"profile"`
	Daemon         string             `json:"daemon"`
	Relay          string             `json:"relay"`
	Storage        string             `json:"storage"`
	ConnectedPeers *int               `json:"connected_peers,omitempty"`
	Invitation     *connectInvitation `json:"invitation,omitempty"`
	Warning        string             `json:"warning"`
}

type connector struct {
	getenv     func(string) string
	environ    func() []string
	executable func() (string, error)
	readConfig func() (daemon.Config, error)
	pinProfile func(daemon.Config)
	label      func() (string, error)
	initialize func(ctx context.Context, relay, label string) error
	call       func(ctx context.Context, request localipc.Request) (json.RawMessage, error)
	spawn      func(executable string, argv, env []string) (<-chan struct{}, error)
	resolve    func(ctx context.Context) (claudeipc.CurrentEvidence, error)
	bind       func(ctx context.Context) (string, error)
	wait       func(ctx context.Context, interval time.Duration) error
}

func connectError(code, state, message string) *localipc.Error {
	problem := localipc.NewError(code, state)
	problem.Message = message
	return problem
}

func errorCode(err error) string { return localipc.PublicError(err).Code }

func (c connector) run(ctx context.Context, options connectOptions) (connectResult, error) {
	if err := c.requireClaudeHost(); err != nil {
		return connectResult{}, err
	}
	profile, profileExists, err := c.resolveRelay(options.relay)
	if err != nil {
		return connectResult{}, err
	}
	if profileExists && c.pinProfile != nil {
		c.pinProfile(profile)
	}
	relay := profile.Relay
	running, err := c.daemonRunning(ctx)
	if err != nil {
		return connectResult{}, err
	}
	if running && !profileExists {
		return connectResult{}, connectError("conflict", "rejected", "A daemon is already serving a different profile. Stop it or use its profile; connect will not initialize a second identity beside it.")
	}
	result := connectResult{Relay: relay, Storage: profile.StorageMode, Profile: "existing", Daemon: "reused", Warning: connectWarning}
	if !profileExists {
		if err = c.initializeProfile(ctx, relay); err != nil {
			return connectResult{}, err
		}
		result.Profile = "initialized"
		profile, err = c.readConfig()
		if err != nil {
			return connectResult{}, err
		}
		if c.pinProfile != nil {
			c.pinProfile(profile)
		}
	}
	if !running {
		if err = c.startDaemon(ctx); err != nil {
			return connectResult{}, err
		}
		result.Daemon = "started"
	}
	status, err := c.status(ctx)
	if err != nil {
		return connectResult{}, err
	}
	if status.ProfileID != daemon.ProfileID(profile.StateDir, profile.Relay) {
		return connectResult{}, connectError("conflict", "rejected", "The running daemon does not match the selected profile, or is an older version. Inspect the selected profile and daemon; connect will not replace them.")
	}
	evidence, action, err := c.ensureBinding(ctx)
	if err != nil {
		return connectResult{}, err
	}
	result.Session, result.BindingAction = evidence.Session, action
	result.Binding, result.State = "native_claude", "ready"
	if options.noInvite {
		return result, nil
	}
	if result.Invitation, err = c.createInvitation(ctx, evidence); err != nil {
		return connectResult{}, err
	}
	result.State = "link_ready"
	result.ConnectedPeers = c.connectedPeers(ctx, evidence)
	return result, nil
}

func (c connector) initializeProfile(ctx context.Context, relay string) error {
	label, err := c.label()
	if err != nil {
		return localipc.PublicError(err)
	}
	return c.initialize(ctx, relay, label)
}

func (c connector) createInvitation(ctx context.Context, evidence claudeipc.CurrentEvidence) (*connectInvitation, error) {
	raw, err := c.call(ctx, localipc.Request{Method: "invite", Session: evidence.Session, Scope: "coauthor", BindingEpoch: evidence.BindingEpoch})
	if err != nil {
		if errorCode(err) == "scope_denied" {
			return nil, connectError("scope_denied", "rejected", "The relay operator must authorize this inviter's public routing_key. Read it with peerturn daemon status; do not edit relay access automatically.")
		}
		return nil, err
	}
	var created struct {
		connectInvitation
		Binding string `json:"binding"`
	}
	if json.Unmarshal(raw, &created) != nil || created.URL == "" || created.Scope != "coauthor" {
		return nil, localipc.NewError("invalid_response", "unavailable")
	}
	if created.Binding != "native_claude" {
		return nil, localipc.NewError("binding_mismatch", "needs_rebind")
	}
	return &created.connectInvitation, nil
}

// Unknown peer counts must not be inferred from invitation creation.
func (c connector) connectedPeers(ctx context.Context, evidence claudeipc.CurrentEvidence) *int {
	raw, err := c.call(ctx, localipc.Request{Method: "peers", Session: evidence.Session, BindingEpoch: evidence.BindingEpoch})
	if err != nil {
		return nil
	}
	var peers []daemon.Peer
	if json.Unmarshal(raw, &peers) != nil {
		return nil
	}
	connected := 0
	for _, peer := range peers {
		if peer.State == "connected" {
			connected++
		}
	}
	return &connected
}

func runConnectWith(ctx context.Context, command cliCommand, stdout, stderr io.Writer, c connector) int {
	options := connectOptions{relay: command.values["relay"], noInvite: command.flags["no-invite"], json: command.flags["json"]}
	result, err := c.run(ctx, options)
	if err != nil {
		return writeFailure(stdout, stderr, err)
	}
	if options.json {
		if json.NewEncoder(stdout).Encode(result) != nil {
			return 1
		}
		return 0
	}
	if _, err = io.WriteString(stdout, result.text()); err != nil {
		return 1
	}
	return 0
}

func runConnect(ctx context.Context, command cliCommand, stdout, stderr io.Writer) int {
	return runConnectWith(ctx, command, stdout, stderr, newConnector())
}

// The complete invitation is intentional user output, never a diagnostic.
func (r connectResult) text() string {
	out := fmt.Sprintf("PeerTurn connect: %s\nsession: %s (%s, %s)\nprofile: %s, daemon: %s, relay: %s, storage: %s\n",
		r.State, r.Session, r.Binding, r.BindingAction, r.Profile, r.Daemon, r.Relay, r.Storage)
	if r.ConnectedPeers != nil {
		out += fmt.Sprintf("connected peers: %d\n", *r.ConnectedPeers)
	}
	out += "warning: " + r.Warning + "\n"
	if r.Invitation == nil {
		return out + "next: paste the invitation you received unchanged into this session, then call peer_join with {}.\n"
	}
	expires := time.Unix(r.Invitation.ExpiresAt, 0).UTC().Format(time.RFC3339)
	return out + fmt.Sprintf("next: give this single-use %s link to the recipient; it is not a connection until they join (expires %s).\n%s\n", r.Invitation.Scope, expires, r.Invitation.URL)
}
