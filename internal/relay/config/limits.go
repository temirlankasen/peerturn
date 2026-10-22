package config

import "errors"

func (l Limits) validate() error {
	if l.TotalBytes < 1<<20 || l.TotalBytes > 1<<40 || l.MailboxBytes < int64(l.CiphertextBytes) || l.MailboxBytes > l.TotalBytes {
		return errors.New("storage byte quotas are inconsistent or outside 1 MiB..1 TiB")
	}
	if l.MailboxItems < 1 || l.MailboxItems > 1000000 || l.MetadataItems < l.MailboxItems || l.MetadataItems > 10000000 {
		return errors.New("item quotas are inconsistent or outside supported bounds")
	}
	if l.CiphertextBytes < 1024 || l.CiphertextBytes > 96<<10 || l.BodyBytes < int64((l.CiphertextBytes+2)/3*4+4096) || l.BodyBytes > 1<<20 {
		return errors.New("ciphertext or JSON body bound is invalid")
	}
	if l.BatchItems < 1 || l.BatchItems > 100 || l.LongPollSeconds < 1 || l.LongPollSeconds > 25 {
		return errors.New("batch or long poll bound is invalid")
	}
	if l.RequestTimeoutSeconds < l.LongPollSeconds+5 || l.RequestTimeoutSeconds > 120 || l.ReadHeaderSeconds < 1 || l.ReadHeaderSeconds > 30 || l.IdleSeconds < 5 || l.IdleSeconds > 300 {
		return errors.New("HTTP timeouts are invalid")
	}
	if l.BodyReadSeconds < 1 || l.BodyReadSeconds > 10 || l.BodyReadSeconds > l.RequestTimeoutSeconds || l.MaxConnections < 1 || l.MaxConnections > 100000 {
		return errors.New("body read timeout or connection cap is invalid")
	}
	if l.RequestsPerIP < 1 || l.RequestsPerIP > 64 || l.IPv6PrefixBits < 48 || l.IPv6PrefixBits > 128 || l.PollsPerKey < 1 || l.PollsPerKey > 32 || l.PollsPerMailbox < 1 || l.PollsPerMailbox > 32 {
		return errors.New("request concurrency or IPv6 aggregation bounds are invalid")
	}
	if l.MaxStreams < 1 || l.MaxStreams > 10000 || l.StreamsPerKey < 1 || l.StreamsPerKey > l.MaxStreams || l.StreamsPerMailbox < 1 || l.StreamsPerMailbox > l.MaxStreams {
		return errors.New("notification resource bounds are invalid")
	}
	if l.RatePerMinute < 1 || l.RatePerMinute > 100000 || l.RateBurst < 1 || l.RateBurst > l.RatePerMinute {
		return errors.New("request rate bounds are invalid")
	}
	return nil
}
