package daemon

import (
	"context"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// queueNativeReceipt journals signed encrypted reverse-channel custody without
// copying native IDs, paths or model text into the peer's receipt evidence.
func (d *Daemon) queueNativeReceipt(ctx context.Context, receipt protocol.ReceiptPayload) error {
	grantID, err := d.db.InboxGrant(ctx, receipt.MessageID)
	if err != nil {
		return err
	}
	var peerID string
	var peer *peerState
	for id, p := range d.state.Peers {
		g, verifyErr := d.incomingGrant(p)
		if verifyErr == nil && g.GrantID == grantID {
			peerID, peer = id, p
			break
		}
	}
	if peer == nil {
		return localipc.NewError("revoked", "revoked")
	}
	if d.state.Reserved != nil {
		if err = d.recoverReserved(ctx); err != nil {
			return err
		}
	}
	receipt.NativeInputID, receipt.NativeTurnID = "", ""
	body, err := codec.Canonical(receipt)
	if err != nil {
		return err
	}
	reserved, err := d.makePacket(peerID, peer, protocol.Receipt, string(body), nil)
	if err != nil {
		return err
	}
	d.state.Reserved = &reserved
	d.state.Sequences[peerID]++
	if err = d.save(); err != nil {
		return err
	}
	if err = d.recoverReserved(ctx); err != nil {
		return err
	}
	d.signal()
	return nil
}
