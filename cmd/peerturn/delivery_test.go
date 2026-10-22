package main

import (
	"strings"
	"testing"
)

func TestDeliveryResolutionArguments(t *testing.T) {
	for _, test := range []struct {
		args    []string
		valid   bool
		outcome string
	}{
		{[]string{"delivery", "resolve", strings.Repeat("a", 32), "--session", "chosen", "--sent"}, true, "sent"},
		{[]string{"delivery", "resolve", strings.Repeat("a", 32), "--session", "chosen", "--not-sent"}, true, "not_sent"},
		{[]string{"delivery", "resolve", strings.Repeat("a", 32), "--session", "chosen"}, false, ""},
		{[]string{"delivery", "resolve", strings.Repeat("a", 32), "--session", "chosen", "--sent", "--not-sent"}, false, ""},
		{[]string{"delivery", "resolve", "invalid", "--session", "chosen", "--sent"}, false, ""},
	} {
		c, err := parseCommand(test.args)
		if err != nil {
			if test.valid {
				t.Fatal(err)
			}
			continue
		}
		r, err := c.request(strings.NewReader(""))
		if test.valid && (err != nil || r.Method != "delivery.resolve" || r.MessageID != strings.Repeat("a", 32) || r.Outcome != test.outcome) {
			t.Fatalf("resolution request: %+v %v", r, err)
		}
		if !test.valid && err == nil {
			t.Fatalf("invalid resolution accepted: %v", test.args)
		}
	}
}

func TestInviteRequiresCurrentAndSessionUnbind(t *testing.T) {
	c, err := parseCommand([]string{"invite", "create", "--session", "chosen", "--scope", "coauthor"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.request(strings.NewReader("")); err == nil {
		t.Fatal("invite without --current accepted")
	}
	c, err = parseCommand([]string{"session", "unbind", "--session", "chosen"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.request(strings.NewReader(""))
	if err != nil || r.Method != "session.unbind" {
		t.Fatalf("unbind %+v %v", r, err)
	}
}
