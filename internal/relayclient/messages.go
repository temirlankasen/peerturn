package relayclient

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/temirlankasen/peerturn/internal/protocol"
)

// Send submits one immutable ciphertext once. Ambiguous failures require caller reconciliation.
func (c *Client) Send(ctx context.Context, packet protocol.EncryptedPacket) (Receipt, error) {
	if packet.Validate() != nil {
		return Receipt{}, invalid()
	}
	var receipt Receipt
	if err := c.do(ctx, "POST", "/v1/mailboxes/"+packet.Header.DstMailbox+"/messages", nil, packet, &receipt, 200); err != nil {
		return Receipt{}, err
	}
	if receipt.Stage != "relay_stored" || receipt.Mailbox != packet.Header.DstMailbox || receipt.MessageID != packet.Header.MessageID || receipt.Seq <= 0 || receipt.Seq > protocol.MaxSafeInteger {
		return Receipt{}, malformed()
	}
	return receipt, nil
}

// Fetch reads a bounded contiguous page, without acknowledging relay custody.
func (c *Client) Fetch(ctx context.Context, mailbox string, opts FetchOptions) ([]Message, error) {
	if opts.Limit == 0 {
		opts.Limit = 100
	}
	if !protocol.ValidToken(mailbox) || opts.After < 0 || opts.After > protocol.MaxSafeInteger || opts.Limit < 1 || opts.Limit > 100 || opts.Wait < 0 || opts.Wait > 25*time.Second || opts.Wait%time.Second != 0 {
		return nil, invalid()
	}
	query := url.Values{"after": {strconv.FormatInt(opts.After, 10)}, "limit": {strconv.Itoa(opts.Limit)}, "wait": {strconv.FormatInt(int64(opts.Wait/time.Second), 10)}}
	var result struct {
		Messages []Message `json:"messages"`
	}
	if err := c.do(ctx, "GET", "/v1/mailboxes/"+mailbox+"/messages", query, nil, &result, 200); err != nil {
		return nil, err
	}
	if result.Messages == nil || len(result.Messages) > opts.Limit {
		return nil, malformed()
	}
	last := opts.After
	for _, message := range result.Messages {
		if message.Seq != last+1 || message.Seq > protocol.MaxSafeInteger || message.Packet == nil && !message.Tombstone || message.Packet != nil && message.Tombstone {
			return nil, malformed()
		}
		if message.Packet != nil && (message.Packet.Validate() != nil || message.Packet.Header.DstMailbox != mailbox) {
			return nil, malformed()
		}
		last = message.Seq
	}
	return result.Messages, nil
}

// Ack advances only to the caller-supplied contiguous durable custody frontier.
func (c *Client) Ack(ctx context.Context, mailbox string, through int64) error {
	if !protocol.ValidToken(mailbox) || through < 0 || through > protocol.MaxSafeInteger {
		return invalid()
	}
	input := struct {
		Through int64 `json:"through"`
	}{through}
	var result struct {
		Acked *int64 `json:"acked"`
	}
	if err := c.do(ctx, "POST", "/v1/mailboxes/"+mailbox+"/ack", nil, input, &result, 200); err != nil {
		return err
	}
	if result.Acked == nil || *result.Acked != through {
		return malformed()
	}
	return nil
}
