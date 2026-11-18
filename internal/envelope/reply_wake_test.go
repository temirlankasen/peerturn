package envelope_test

import (
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// replyOptions continues a conversation the receiver started, under the given
// recipient-issued scope, so only the grant decides whether the reply wakes it.
func replyOptions(t *testing.T, scope grant.Scope) (party, protocol.RoutingHeader, protocol.MessagePayload, envelope.OpenOptions) {
	t.Helper()
	s, h, m, o := setup(t)
	r := newParty(t, "recipient")
	g := grant.Grant{Version: 1, GrantID: m.GrantID, Issuer: r.cert.Binding, Subject: s.cert.Binding,
		Scope: scope, GrantEpoch: 1, NotBefore: now - 5, ExpiresAt: now + 600,
		Limits: grant.DefaultLimits(), ReplyRoute: &s.cert.Binding}
	var err error
	o.SignedGrant, err = grant.Issue(g, r.signing)
	if err != nil {
		t.Fatal(err)
	}
	o.RecipientKey, o.RecipientChain, o.RecipientPin = r.encryption, r.chain, r.cert.IdentityID
	m.GrantHash = grant.Hash(o.SignedGrant)
	m.Kind = protocol.Reply
	m.ParentID = strings.Repeat("c", 32)
	m.ReplyTo = m.ParentID
	m.Hop = 1
	o.Conversation = &envelope.Conversation{ID: m.ConversationID, ParentID: m.ParentID, BudgetID: m.BudgetID, Peer: s.cert.Binding}
	return s, h, m, o
}

func TestCoauthorReplyWakesTheReceivingSession(t *testing.T) {
	s, h, m, o := replyOptions(t, grant.Coauthor)
	a, err := envelope.Open(signedPacket(t, s, h, m, o), o)
	if err != nil || a.Routing != envelope.Live || !a.WantsModel {
		t.Fatalf("coauthor reply stayed silent: %+v %v", a, err)
	}
}

func TestAskReplyDoesNotGainLiveAuthority(t *testing.T) {
	s, h, m, o := replyOptions(t, grant.Ask)
	a, err := envelope.Open(signedPacket(t, s, h, m, o), o)
	if err != nil || a.Routing != envelope.InboxNoWake || a.WantsModel {
		t.Fatalf("ask reply woke the session: %+v %v", a, err)
	}
}

func TestWakingReplySpendsTheTurnBudget(t *testing.T) {
	s, h, m, o := replyOptions(t, grant.Coauthor)
	o.Budget = envelope.BudgetSnapshot{MinuteStart: now, TurnWindowStart: now - 1,
		TurnsInTenMinutes: grant.DefaultLimits().MaxTurnsPerTenMinutes}
	if _, err := envelope.Open(signedPacket(t, s, h, m, o), o); !fault.IsCode(err, fault.BudgetExceeded) {
		t.Fatalf("waking reply bypassed the turn budget: %v", err)
	}
}

func TestReplyChainStopsAtTheHopLimit(t *testing.T) {
	s, h, m, o := replyOptions(t, grant.Coauthor)
	o.Conversation.Hop = grant.DefaultLimits().MaxHops
	m.Hop = o.Conversation.Hop + 1
	if _, err := envelope.Open(signedPacket(t, s, h, m, o), o); !fault.IsCode(err, fault.BudgetExceeded) {
		t.Fatalf("automatic reply chain did not end: %v", err)
	}
}
