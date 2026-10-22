package daemon

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

type informNotice struct {
	id       string
	messages []string
	expires  time.Time
}

func (d *Daemon) claudeBoundary(ctx context.Context) (any, error) {
	items, err := d.claudeEligible(ctx, envelope.InboxNoWake)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return map[string]string{"notice_id": "", "context": ""}, nil
	}
	if len(items) > 20 {
		items = items[:20]
	}
	notice := &informNotice{id: randomID(), expires: time.Now().Add(10 * time.Second)}
	var labels []string
	for _, item := range items {
		sender, senderErr := d.claudeSender(item)
		if senderErr != nil {
			return nil, senderErr
		}
		notice.messages = append(notice.messages, item.MessageID)
		if !slices.Contains(labels, sender) {
			labels = append(labels, sender)
		}
	}
	slices.Sort(labels)
	d.claudeNotice = notice
	text := fmt.Sprintf("PeerTurn: %d inform message(s) from %s. Use peer_inbox or peerturn inbox to read bodies.", len(items), strings.Join(labels, ", "))
	return map[string]string{"notice_id": notice.id, "context": text}, nil
}

func (d *Daemon) claudeBoundaryAttached(ctx context.Context, r localipc.Request) (any, error) {
	notice := d.claudeNotice
	if notice == nil || r.Nonce != notice.id || !time.Now().Before(notice.expires) {
		return nil, localipc.NewError("invalid_state", "pending")
	}
	var ids []string
	for _, id := range notice.messages {
		item, err := d.db.InboxEntry(ctx, id)
		if err != nil {
			if terminalClaudeError(err) || localipc.PublicError(err).Code == "not_found" {
				continue
			}
			return nil, err
		}
		if _, err = d.claudeSender(item); err != nil {
			if err = d.rejectClaudeItem(ctx, id, err); err != nil {
				return nil, err
			}
			continue
		}
		ids = append(ids, id)
	}
	if err := d.commitInform(ctx, ids, "offered", notice.id); err != nil {
		return nil, err
	}
	d.claudeNotice = nil
	return map[string]bool{"attached": true}, nil
}
