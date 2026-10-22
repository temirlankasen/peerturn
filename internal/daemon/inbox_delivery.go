package daemon

import (
	"context"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func (d *Daemon) inboxDelivered(ctx context.Context, id string) (any, error) {
	item, err := d.db.Inbox(ctx, id)
	if err != nil {
		return nil, err
	}
	if item.Routing != envelope.InboxNoWake {
		return nil, localipc.NewError("scope_denied", "rejected")
	}
	if item.State != "delivered" {
		if err = d.commitInform(ctx, []string{id}, "delivered", ""); err != nil {
			return nil, err
		}
	}
	return map[string]bool{"delivered": true}, nil
}
