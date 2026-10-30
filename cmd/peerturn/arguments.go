package main

import (
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

type cliCommand struct {
	name   string
	values map[string]string
	flags  map[string]bool
	id     string
}

func localCommand(name string) bool {
	switch name {
	case "init", "daemon", "invite", "join", "peers", "send", "reply", "inbox", "receipt", "status", "grants", "doctor", "mcp", "session", "adapter", "delivery", "keys", "connect":
		return true
	case "codex-hook":
		return true
	default:
		return false
	}
}

func parseCommand(args []string) (cliCommand, error) {
	c := cliCommand{name: args[0], values: map[string]string{}, flags: map[string]bool{}}
	args = args[1:]
	if c.name == "daemon" || c.name == "invite" || c.name == "grants" || c.name == "session" || c.name == "delivery" || c.name == "keys" {
		if len(args) == 0 {
			return c, invalidArguments()
		}
		c.name += " " + args[0]
		args = args[1:]
	}
	allowed, ok := commandFlags[c.name]
	if !ok {
		return c, invalidArguments()
	}
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		if !strings.HasPrefix(arg, "--") {
			if c.id != "" || (c.name != "receipt" && c.name != "grants revoke" && c.name != "delivery resolve") {
				return c, invalidArguments()
			}
			c.id = arg
			continue
		}
		name := strings.TrimPrefix(arg, "--")
		mode, found := allowed[name]
		if !found || c.flags[name] || c.values[name] != "" {
			return c, invalidArguments()
		}
		if mode {
			c.flags[name] = true
			continue
		}
		if len(args) == 0 || args[0] == "" || strings.HasPrefix(args[0], "--") {
			return c, invalidArguments()
		}
		c.values[name], args = args[0], args[1:]
	}
	return c, nil
}

var commandFlags = map[string]map[string]bool{
	"keys isolate":     {"state-dir": false, "json": true},
	"session bind":     {"current": true, "json": true},
	"session unbind":   {"session": false, "current": true, "json": true},
	"delivery resolve": {"session": false, "current": true, "json": true, "sent": true, "not-sent": true},
	"init":             {"relay": false, "relay-key": false, "session": false, "state-dir": false, "file-permissions": true, "codex-current": true},
	"codex-hook":       {},
	"daemon start":     {"state-dir": false},
	"daemon stop":      {"json": true},
	"daemon status":    {"json": true},
	"invite create":    {"session": false, "current": true, "scope": false, "json": true},
	"join":             {"session": false, "current": true, "scope": false, "link-stdin": true, "json": true},
	"peers":            {"session": false, "current": true, "json": true},
	"send":             {"session": false, "current": true, "peer": false, "kind": false, "stdin": true, "json": true},
	"reply":            {"session": false, "current": true, "message": false, "stdin": true, "json": true},
	"inbox":            {"session": false, "current": true, "json": true, "after": false},
	"receipt":          {"session": false, "current": true, "message": false, "json": true},
	"status":           {"session": false, "current": true, "json": true},
	"grants list":      {"session": false, "current": true, "json": true},
	"grants revoke":    {"session": false, "current": true, "grant": false, "json": true},
	"doctor":           {"session": false, "current": true, "json": true},
	"mcp":              {"session": false, "current": true},
	"connect":          {"relay": false, "no-invite": true, "json": true},
}

func invalidArguments() error { return localipc.NewError("invalid_arguments", "rejected") }

func (c cliCommand) request(stdin io.Reader) (localipc.Request, error) {
	r := localipc.Request{Method: c.name, Session: c.values["session"], Peer: c.values["peer"], Kind: c.values["kind"], Scope: c.values["scope"], MessageID: c.values["message"], GrantID: c.values["grant"]}
	r.Cursor = c.values["after"]
	thread := os.Getenv("CODEX_THREAD_ID")
	if thread != "" && (r.Session == "" || r.Session == thread) {
		r.Session, r.NativeThread = thread, thread
	}
	switch c.name {
	case "daemon status":
		r.Method = "status"
	case "daemon stop":
		r.Method = "stop"
	case "invite create":
		r.Method = "invite"
	case "grants list":
		r.Method = "grants"
	case "grants revoke":
		r.Method = "revoke"
	case "session unbind":
		r.Method = "session.unbind"
	case "delivery resolve":
		r.Method = "delivery.resolve"
		if c.flags["sent"] == c.flags["not-sent"] || !protocol.ValidID(c.id) {
			return r, invalidArguments()
		}
		r.MessageID, r.Outcome = c.id, "not_sent"
		if c.flags["sent"] {
			r.Outcome = "sent"
		}
	}
	if r.Session == "" && r.Method != "status" && r.Method != "doctor" && r.Method != "stop" && r.Method != "delivery.resolve" && r.Method != "session.unbind" {
		return r, localipc.NewError("unbound", "unbound")
	}
	if c.flags["current"] && r.Session == "" {
		return r, localipc.NewError("unbound", "unbound")
	}
	if c.name == "invite create" && !c.flags["current"] {
		return r, invalidArguments()
	}
	if r.Method == "invite" || r.Method == "join" {
		if r.Scope != "coauthor" {
			return r, invalidArguments()
		}
	}
	if r.Method == "send" && (r.Peer == "" || (r.Kind != "coauthor" && r.Kind != "ask" && r.Kind != "inform")) {
		return r, invalidArguments()
	}
	if r.Method == "send" || r.Method == "reply" {
		if !c.flags["stdin"] {
			return r, invalidArguments()
		}
		text, err := readBounded(stdin, 16*1024)
		if err != nil || text == "" {
			return r, invalidArguments()
		}
		r.Body = text
	}
	if r.Method == "join" {
		if !c.flags["link-stdin"] {
			return r, invalidArguments()
		}
		return invitationRequest(stdin, r)
	}
	if r.Method == "receipt" && c.id != "" {
		if r.MessageID != "" {
			return r, invalidArguments()
		}
		r.MessageID = c.id
	}
	if r.Method == "revoke" && c.id != "" {
		if r.GrantID != "" {
			return r, invalidArguments()
		}
		r.GrantID = c.id
	}
	if (r.Method == "reply" || r.Method == "receipt") && r.MessageID == "" {
		return r, invalidArguments()
	}
	if r.Method == "revoke" && r.GrantID == "" {
		return r, invalidArguments()
	}
	return r, r.Validate()
}

func readBounded(reader io.Reader, limit int64) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(data)) > limit || !utf8.Valid(data) {
		return "", invalidArguments()
	}
	return string(data), nil
}
