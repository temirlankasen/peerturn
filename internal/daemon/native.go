package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/temirlankasen/peerturn/internal/adapters/codex"
	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

// NativeRuntime is deliberately limited to exact binding and queued coauthor
// submission. Restricted asks cannot accidentally reach this interface.
type NativeRuntime interface {
	Bind(context.Context, string) (codex.Binding, error)
	Validate(context.Context, codex.Binding) error
	Queue(context.Context, codex.Binding, string, string) (codex.QueueResult, error)
	Delete(context.Context, codex.Binding, string) (bool, error)
}

func (d *Daemon) runtime(ctx context.Context) (NativeRuntime, error) {
	if d.native != nil {
		return d.native, nil
	}
	path, err := codex.DefaultSocketPath()
	if err != nil {
		return nil, nativeError(err)
	}
	t, err := codex.Open(ctx, path)
	if err != nil {
		return nil, nativeError(err)
	}
	d.native = t
	return t, nil
}

func nativeError(err error) error {
	if e, ok := errors.AsType[*codex.Error](err); ok {
		return localipc.NewError(e.Code, e.Code)
	}
	return localipc.NewError("adapter_unavailable", "needs_rebind")
}

func bindingContext(s clientState) []byte {
	if s.Codex == nil {
		return []byte("test-session:" + s.Label)
	}
	raw, err := codec.Canonical(s.Codex)
	if err != nil {
		return nil
	}
	return raw
}

func initializeNative(ctx context.Context, s *clientState, o InitOptions) error {
	if o.CodexThreadID == "" {
		return nil
	}
	d := &Daemon{native: o.Native}
	r, err := d.runtime(ctx)
	if err != nil {
		return err
	}
	if o.Native == nil {
		defer func() {
			if c, ok := r.(interface{ Close() error }); ok {
				if closeErr := c.Close(); closeErr != nil {
					return
				}
			}
		}()
	}
	binding, err := r.Bind(ctx, o.CodexThreadID)
	if err != nil {
		return nativeError(err)
	}
	s.Codex = &binding
	return nil
}

func (d *Daemon) checkNative(ctx context.Context, thread string) error {
	if d.state.Codex == nil {
		if thread != "" {
			return localipc.NewError("binding_error", "needs_rebind")
		}
		return nil
	}
	if thread != "" && thread != d.state.Codex.ThreadID {
		return localipc.NewError("binding_mismatch", "needs_rebind")
	}
	r, err := d.runtimeLocked(ctx)
	if err != nil {
		return err
	}
	binding := *d.state.Codex
	checkCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	d.mu.Unlock()
	err = r.Validate(checkCtx, binding)
	d.mu.Lock()
	if err != nil {
		return nativeError(err)
	}
	return nil
}

func (d *Daemon) nativeStatus(ctx context.Context, s *Status) {
	if d.state.Codex == nil {
		return
	}
	s.Binding, s.NativeThread = "codex_shared_daemon", d.state.Codex.ThreadID
	s.ResponderReason = "protected_path_policy_failed"
	s.Warning = "Coauthor is strong trust with the receiving agent's normal capabilities, not a sandbox. Peer text can influence later owner turns. Ask is disabled until authority isolation and budget qualification."
	count, err := d.db.NativePendingCount(ctx)
	if err == nil {
		s.InformPending = count
	}
	s.InformBoundary = "unverified"
	if err = d.checkNative(ctx, ""); err != nil {
		s.State, s.AdapterReason = "needs_rebind", safeCode(err)
		return
	}
	probe, ok := d.native.(interface {
		Probe(context.Context, string) (codex.Binding, codex.Capabilities, error)
	})
	if !ok {
		s.AdapterReason = "unverified"
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	d.mu.Unlock()
	_, caps, err := probe.Probe(probeCtx, s.NativeThread)
	d.mu.Lock()
	if err != nil || !caps.WakeIdle {
		s.AdapterReason = "adapter_unavailable"
		return
	}
	s.Capabilities.AttachExisting, s.Capabilities.WakeIdle = true, true
	s.Capabilities.ReceiptStages = append(s.Capabilities.ReceiptStages, "adapter_accepted", "offered", "delivered", "in_doubt")
	s.SupportedScopes = []string{"coauthor"}
	if d.hookVerified > 0 {
		s.Capabilities.BoundaryDelivery = true
		s.Capabilities.InformBoundary = true
		s.SupportedScopes = append(s.SupportedScopes, "inform")
		s.InformBoundary = "observed"
	}
}

func needsLiveBinding(method string) bool {
	switch method {
	case "hello", "invite", "join", "join.text", "send", "reply":
		return true
	}
	return false
}

func bindingLabel(s clientState) string {
	if s.Codex != nil {
		return "codex_shared_daemon"
	}
	return "test_session"
}

func (d *Daemon) nativeMessage(ctx context.Context, item store.NativeItem) (protocol.MessagePayload, string, *peerState, error) {
	if item.Error != nil {
		return protocol.MessagePayload{}, "", nil, item.Error
	}
	packet, err := protocol.DecodePacket(item.Packet)
	if err != nil {
		return protocol.MessagePayload{}, "", nil, err
	}
	for id, peer := range d.state.Peers {
		if grant.Hash(peer.Incoming) != item.GrantHash || peer.IncomingRevoked {
			continue
		}
		// Custody already verified the exact grant/certificates at receipt time.
		// Re-open the retained signature using that installation, not today's
		// cached chain or the independent outgoing grant's revoked projection.
		aad, headerErr := packet.Header.Canonical()
		if headerErr != nil {
			return protocol.MessagePayload{}, id, peer, headerErr
		}
		compact, openErr := peercrypto.Open(d.key, packet.Enc, aad, packet.Ciphertext)
		if openErr != nil {
			return protocol.MessagePayload{}, id, peer, openErr
		}
		key := item.Grant.Subject.SigningKey
		raw, verifyErr := peercrypto.Verify(string(compact), identity.RootFingerprint(key), peercrypto.MessageType, key)
		if verifyErr != nil {
			return protocol.MessagePayload{}, id, peer, verifyErr
		}
		var signed envelope.SignedMessage
		if err = codec.Decode(raw, protocol.MaxPlaintext, &signed); err != nil {
			return protocol.MessagePayload{}, id, peer, err
		}
		m := signed.Message
		if signed.Header != packet.Header || m.MessageID != item.MessageID || m.GrantID != item.GrantID || m.GrantHash != item.GrantHash || m.Validate() != nil {
			return m, id, peer, localipc.NewError("invalid_input", "rejected")
		}
		return m, id, peer, nil
	}
	return protocol.MessagePayload{}, "", nil, localipc.NewError("revoked", "revoked")
}

// deliverNative runs behind the same operation fence as revoke and persists intent
// before queue/add. Every uncertain result is terminal until explicit reconciliation.
func (d *Daemon) deliverNative(ctx context.Context) error {
	if d.state.Codex == nil {
		return nil
	}
	if err := d.checkNative(ctx, ""); err != nil {
		return err
	}
	items, err := d.db.NativePendingRoute(ctx, 100, envelope.Live)
	if err != nil {
		return err
	}
	var outcomeErr error
	for _, item := range items {
		if item.Routing != envelope.Live {
			continue
		}
		m, id, peer, readErr := d.nativeMessage(ctx, item)
		if readErr != nil {
			if err = d.db.RejectNative(ctx, item.MessageID, safeCode(readErr), unixNow()); err != nil {
				return err
			}
			continue
		}
		frame, frameErr := codex.Frame(m, item.Grant.Subject.IdentityID)
		if frameErr != nil {
			if err = d.db.RejectNative(ctx, item.MessageID, "invalid_frame", unixNow()); err != nil {
				return err
			}
			continue
		}
		attempt, attemptErr := d.db.DeliveryIntent(ctx, item.MessageID, randomID(), unixNow())
		if attemptErr != nil {
			return attemptErr
		}
		if !attempt.Start {
			continue
		}
		if err = d.db.AuthorizeDelivery(ctx, item.MessageID, attempt.ID, unixNow()); err != nil {
			if terminalClaudeError(err) {
				if rejectErr := d.rejectClaudeItem(ctx, item.MessageID, err); rejectErr != nil {
					return rejectErr
				}
				continue
			}
			return err
		}
		binding, runtime := *d.state.Codex, d.native
		d.recordPeerInvites(id, m.BodyUTF8)
		result, submitErr := networkCall(ctx, d, func() (codex.QueueResult, error) { return runtime.Queue(ctx, binding, item.MessageID, frame) })
		r := protocol.ReceiptPayload{MessageID: item.MessageID, AttemptID: attempt.ID, At: unixNow(), Stage: "adapter_accepted", EvidenceKind: "codex_queue"}
		if submitErr != nil {
			r.Stage, r.ReasonCode = "in_doubt", safeCode(nativeError(submitErr))
		} else if len(result.ItemIDs) > 0 {
			r.NativeInputID = result.ItemIDs[0]
		}
		raw, encodeErr := codec.Canonical(result)
		if encodeErr != nil {
			return encodeErr
		}
		wire, reserveErr := d.reserveNativeReceipt(id, peer, r)
		var notifications []store.NativeNotification
		if reserveErr == nil {
			notifications = append(notifications, wire)
		} else if retryCustody(reserveErr) || d.persistenceFailed {
			return reserveErr
		}
		if err = d.db.DeliveryResult(ctx, attempt.ID, r, raw, notifications...); err != nil {
			if dropErr := d.dropNativeReservation(ctx); dropErr != nil {
				return dropErr
			}
			return err
		}
		if err = d.recoverReserved(ctx); err != nil {
			return err
		}
		if submitErr != nil && outcomeErr == nil {
			outcomeErr = nativeError(submitErr)
		}
	}
	return outcomeErr
}

// Callers hold mu. Opening the native transport must not stall owner inspection.
func (d *Daemon) runtimeLocked(ctx context.Context) (NativeRuntime, error) {
	if d.native != nil {
		return d.native, nil
	}
	d.mu.Unlock()
	path, err := codex.DefaultSocketPath()
	var runtime *codex.Transport
	if err == nil {
		runtime, err = codex.Open(ctx, path)
	}
	d.mu.Lock()
	if err != nil {
		return nil, nativeError(err)
	}
	if d.native == nil {
		d.native = runtime
	} else if closeErr := runtime.Close(); closeErr != nil {
		return nil, nativeError(closeErr)
	}
	return d.native, nil
}

func (d *Daemon) reserveNativeReceipt(id string, p *peerState, r protocol.ReceiptPayload) (store.NativeNotification, error) {
	if d.state.Reserved != nil {
		return store.NativeNotification{}, localipc.NewError("backpressure", "pending")
	}
	r.NativeInputID, r.NativeTurnID = "", ""
	body, err := codec.Canonical(r)
	if err != nil {
		return store.NativeNotification{}, err
	}
	reserved, err := d.makePacket(id, p, protocol.Receipt, string(body), nil)
	if err != nil {
		return store.NativeNotification{}, err
	}
	d.state.Reserved = &reserved
	d.state.Sequences[id]++
	if err = d.save(); err != nil {
		return store.NativeNotification{}, err
	}
	wire, err := codec.Canonical(reserved.Packet)
	return store.NativeNotification{GrantID: reserved.Message.GrantID, Packet: wire}, err
}
