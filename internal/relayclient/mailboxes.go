package relayclient

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
)

// EnrollMailbox enrolls one reader and consumes a voucher only on the relay.
func (c *Client) EnrollMailbox(ctx context.Context, input MailboxRequest) (string, error) {
	if !protocol.ValidToken(input.ID) || !auth.ValidRoutingKey(input.ReadKey) || len(input.Voucher) > 256 {
		return "", invalid()
	}
	var result struct {
		Mailbox string `json:"mailbox"`
	}
	if err := c.do(ctx, "POST", "/v1/mailboxes", nil, input, &result, 201); err != nil {
		return "", err
	}
	if result.Mailbox != input.ID {
		return "", malformed()
	}
	return result.Mailbox, nil
}

// DeleteMailbox removes ciphertext custody using the mailbox reader's signature.
func (c *Client) DeleteMailbox(ctx context.Context, id string) error {
	if !protocol.ValidToken(id) {
		return invalid()
	}
	var result struct {
		Deleted bool `json:"deleted"`
	}
	if err := c.do(ctx, "DELETE", "/v1/mailboxes/"+id, nil, nil, &result, 200); err != nil {
		return err
	}
	if !result.Deleted {
		return malformed()
	}
	return nil
}

// PutPermit grants transport writing, independent of encrypted application grants.
func (c *Client) PutPermit(ctx context.Context, mailbox, id string, permit Permit) error {
	if !protocol.ValidToken(mailbox) || !protocol.ValidToken(id) || !auth.ValidRoutingKey(permit.WriterKey) || permit.Epoch <= 0 || permit.Epoch >= protocol.MaxSafeInteger || !validExpiry(permit.ExpiresAt) {
		return invalid()
	}
	return c.permit(ctx, "PUT", mailbox, id, permit, permit.Epoch)
}

// RevokePermit retains the relay's monotonic revocation tombstone.
func (c *Client) RevokePermit(ctx context.Context, mailbox, id string, epoch int64) error {
	if !protocol.ValidToken(mailbox) || !protocol.ValidToken(id) || epoch <= 0 || epoch > protocol.MaxSafeInteger {
		return invalid()
	}
	return c.permit(ctx, "DELETE", mailbox, id, struct {
		Epoch int64 `json:"epoch"`
	}{epoch}, epoch)
}

func (c *Client) permit(ctx context.Context, method, mailbox, id string, input any, epoch int64) error {
	var result struct {
		Epoch int64 `json:"epoch"`
	}
	if err := c.do(ctx, method, "/v1/mailboxes/"+mailbox+"/permits/"+id, nil, input, &result, 200); err != nil {
		return err
	}
	if result.Epoch != epoch {
		return malformed()
	}
	return nil
}

// MintTicket returns a notify-only one-use ticket; the client never logs it.
func (c *Client) MintTicket(ctx context.Context, mailbox string) (Ticket, error) {
	if !protocol.ValidToken(mailbox) {
		return Ticket{}, invalid()
	}
	var result Ticket
	input := struct {
		Mailbox string `json:"mailbox"`
	}{mailbox}
	if err := c.do(ctx, "POST", "/v1/streams/tickets", nil, input, &result, 201); err != nil {
		return Ticket{}, err
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(result.Ticket)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != result.Ticket || result.ExpiresAt <= 0 || result.ExpiresAt > protocol.MaxSafeInteger {
		return Ticket{}, malformed()
	}
	return result, nil
}

func validExpiry(expires int64) bool {
	return expires > time.Now().Unix() && expires <= (1<<63-1)/int64(time.Second)
}
