package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/store"
)

// Dispatch checks the explicit test binding and keeps MCP capabilities separate
// from trusted same-uid CLI owner operations. It never accepts raw keys or routes.
func (d *Daemon) Dispatch(ctx context.Context, r localipc.Request) (any, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	d.touchClaudeLease(r)
	d.recordClaudeOwnerInvites(ctx, r)
	if needsOperationLock(r.Method) {
		if err := d.lockOperation(ctx); err != nil {
			return nil, err
		}
		defer d.opMu.Unlock()
		ctx = context.WithValue(ctx, operationLockKey{}, d)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.ProfileID != "" && r.ProfileID != ProfileID(d.options.StateDir, d.state.RelayURL) {
		return nil, localipc.NewError("conflict", "rejected")
	}
	d.expireCapabilities()
	if strings.HasPrefix(r.Method, "adapter.") {
		if r.Capability != "" || d.persistenceFailed {
			return nil, localipc.NewError("scope_denied", "rejected")
		}
		return d.claudeDispatch(ctx, r)
	}
	if err := d.sessionCheck(r); err != nil {
		return nil, err
	}
	if (r.NativeThread != "" || d.state.Codex != nil) && needsLiveBinding(r.Method) {
		if d.state.Codex != nil && r.NativeThread == "" && r.Capability == "" {
			return nil, localipc.NewError("unbound", "unbound")
		}
		if err := d.checkNative(ctx, r.NativeThread); err != nil {
			return nil, err
		}
	}
	if d.persistenceFailed && r.Method != "status" && r.Method != "stop" && r.Method != "bye" {
		return nil, fault.New(store.StorageUnavailable)
	}
	if r.Capability != "" {
		switch r.Method {
		case "peers", "join.owner", "join.text", "send", "reply", "inbox", "inbox.delivered", "receipt", "status", "bye":
		default:
			return nil, fault.New(fault.ScopeDenied)
		}
	}
	switch r.Method {
	case "hello":
		return d.newCapability(ctx, r)
	case "bye":
		if r.Capability == "" {
			return nil, fault.New(fault.ScopeDenied)
		}
		delete(d.capabilities, r.Capability)
		return map[string]string{"state": "released"}, nil
	case "status":
		state := "offline"
		if d.verified > 0 && d.lastError == "" {
			state = "active"
		}
		for _, p := range d.state.Peers {
			if p.SequenceBlocked && !p.IncomingRevoked && !p.Revoked {
				state = "needs_rebind"
				break
			}
		}
		if d.persistenceFailed {
			state = "store_unavailable"
		}
		status := statusFor(d.state, state, d.verified, d.lastError)
		status.ProfileID = ProfileID(d.options.StateDir, d.state.RelayURL)
		if err := d.claudeStatus(ctx, &status); err != nil {
			return nil, err
		}
		status.Notifications = "unavailable"
		if d.notifyOnline {
			status.Notifications = "online"
		}
		d.nativeStatus(ctx, &status)
		d.responderStatus(ctx, &status)
		finishRuntimeReport(&status)
		if r.Capability != "" && d.checkJoinCapability(r.Capability) == nil {
			status.OwnerInvitations = d.pendingOwnerInvites(d.capabilities[r.Capability].nativeEpoch)
		}
		return status, nil
	case "codex_inform":
		return d.offerInform(ctx, r)
	case "codex_inform_ack":
		return d.acknowledgeInform(ctx, r)
	case "codex_peer_started":
		return d.nativePeerStarted(ctx, r)
	case "doctor":
		d.mu.Unlock()
		_, err := d.relay.Discovery(ctx)
		d.mu.Lock()
		if err != nil {
			return nil, err
		}
		diagnostic := map[string]any{"relay": "reachable", "e2e": true, "storage": "file_permissions", "binding": "test_session", "native_adapters": "unavailable", "guard": "unavailable", "inform": "disabled", "ask": "disabled", "coauthor": "strong_trust"}
		if d.state.NativeSessionID != "" {
			status := statusFor(d.state, "active", d.verified, d.lastError)
			if statusErr := d.claudeStatus(ctx, &status); statusErr != nil {
				return nil, statusErr
			}
			d.responderStatus(ctx, &status)
			finishRuntimeReport(&status)
			diagnostic["runtime_policy"] = status.RuntimePolicy
			diagnostic["binding"], diagnostic["capabilities"], diagnostic["runtime_version"], diagnostic["provider"] = status.Binding, status.Capabilities, status.RuntimeVersion, status.Provider
			diagnostic["inform"], diagnostic["ask"] = "owner_boundary", "responder_unavailable"
			if status.Capabilities.ForkResponder {
				diagnostic["ask"] = "qualified_claude_fork"
			}
			if status.Binding == "native_claude" {
				diagnostic["native_adapters"] = "claude"
			}
		}
		if d.state.Codex != nil {
			status := statusFor(d.state, "active", d.verified, d.lastError)
			d.nativeStatus(ctx, &status)
			finishRuntimeReport(&status)
			diagnostic["runtime_policy"], diagnostic["runtime_version"] = status.RuntimePolicy, status.RuntimeVersion
			diagnostic["binding"], diagnostic["capabilities"] = status.Binding, status.Capabilities
			diagnostic["adapter_reason"], diagnostic["inform_boundary"] = status.AdapterReason, status.InformBoundary
			diagnostic["inform"], diagnostic["ask"] = "owner_boundary", "responder_unavailable"
			if status.Capabilities.AttachExisting {
				diagnostic["native_adapters"] = "codex_shared_daemon"
			}
		}
		diagnostic["responder"], diagnostic["storage"] = d.responderPreflight, d.responderPreflight.StorageMode
		return diagnostic, nil
	case "peers":
		return d.peers(), nil
	case "invite":
		if r.Scope != "coauthor" {
			return nil, fault.New(fault.ScopeDenied)
		}
		return d.createInvite(ctx)
	case "join":
		if r.Scope != "coauthor" {
			return nil, fault.New(fault.ScopeDenied)
		}
		return d.join(ctx, r.Link)
	case "join.owner":
		if r.Scope != "coauthor" {
			return nil, fault.New(fault.ScopeDenied)
		}
		return d.joinOwnerInvitation(ctx, r)
	case "join.text":
		if r.Scope != "coauthor" {
			return nil, fault.New(fault.ScopeDenied)
		}
		if err := d.checkJoinCapability(r.Capability); err != nil {
			return nil, err
		}
		link, err := invitationText(r.InvitationText)
		if err != nil {
			return nil, err
		}
		if r.Capability == "" {
			return d.join(ctx, link)
		}
		if err = d.checkInvitationOrigin(link, r.Capability); err != nil {
			return nil, err
		}
		hash, _, err := invite.OriginHashes(link)
		if err != nil {
			return nil, err
		}
		r.InvitationHandle = d.joinOrigins.owner[hash].handle
		return d.joinOwnerInvitation(ctx, r)
	case "send", "reply":
		return d.send(ctx, r)
	case "inbox":
		return d.inbox(ctx, r.Cursor)
	case "inbox.delivered":
		return d.inboxDelivered(ctx, r.MessageID)
	case "delivery.resolve":
		if r.Outcome != "sent" && r.Outcome != "not_sent" {
			return nil, localipc.NewError("invalid_arguments", "rejected")
		}
		// A missing queue item may already have started. The pinned Codex API
		// cannot prove non-start for an ambiguous queue/add, so never re-enqueue it.
		if d.state.Codex != nil && r.Outcome == "not_sent" {
			return nil, localipc.NewError("in_doubt", "in_doubt")
		}
		receipt, err := d.db.ResolveDelivery(ctx, r.MessageID, r.Outcome == "sent", unixNow())
		if err == nil {
			if d.claudeAttempt != nil && d.claudeAttempt.MessageID == r.MessageID {
				d.claudeAttempt = nil
			}
			err = d.queueClaudeReceipt(ctx, receipt)
		}
		return receipt, err
	case "session.unbind":
		return d.claudeUnbind(ctx)
	case "receipt":
		return d.db.Receipt(ctx, r.MessageID)
	case "grants":
		grants, err := d.db.GrantCatalog(ctx)
		for i := range grants {
			for _, peer := range d.state.Peers {
				// The authenticated peer projection survives receipt retention.
				if peer.Revoked && grants[i].Hash == grant.Hash(peer.Outgoing) {
					grants[i].EffectiveRevoked = true
				}
			}
		}
		if grants == nil {
			grants = []store.GrantInfo{}
		}
		return grants, err
	case "revoke":
		return d.revoke(ctx, r.GrantID)
	case "stop":
		if d.cancel != nil {
			cancel := d.cancel
			time.AfterFunc(50*time.Millisecond, cancel)
		}
		return map[string]string{"state": "stopping"}, nil
	default:
		return nil, localipc.NewError("unsupported", "unsupported")
	}
}

func (d *Daemon) inbox(ctx context.Context, cursor string) (any, error) {
	items, err := d.db.ListInboxAfter(ctx, cursor, 25)
	if err != nil {
		return nil, err
	}
	result := InboxPage{Messages: []InboxMessage{}}
	grants, err := d.db.ListGrants(ctx)
	if err != nil {
		return nil, err
	}
	size := 0
	for _, item := range items {
		peerID := ""
		for id, p := range d.state.Peers {
			for _, g := range grants {
				if g.GrantID == item.GrantID && g.Hash == grant.Hash(p.Incoming) {
					peerID = id
					break
				}
			}
		}
		message := InboxMessage{MessageID: item.MessageID, Peer: peerID, BodyUTF8: string(item.Body), Routing: string(item.Routing), State: item.State, Stage: "receiver_stored"}
		if item.State == "delivered" || item.State == "offered" {
			message.Stage = item.State
		}
		encoded, encodeErr := json.Marshal(message)
		if encodeErr != nil {
			return nil, encodeErr
		}
		if len(result.Messages) >= 24 || size+len(encoded) > 512*1024 {
			result.NextCursor = result.Messages[len(result.Messages)-1].MessageID
			break
		}
		result.Messages = append(result.Messages, message)
		d.recordPeerInvites(peerID, message.BodyUTF8)
		size += len(encoded)
	}
	return result, nil
}
