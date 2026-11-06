package main

import (
	"io"
	"strings"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

// One usage line per command keeps `--help` and an invalid_arguments error
// answerable without reading the README: the error names the exact syntax.
var commandUsage = map[string]string{
	"init":             "peerturn init --relay URL --session LABEL --file-permissions [--relay-key KEY] [--state-dir DIR] [--codex-current]\n  Initialize a profile. Claude sessions use peerturn connect instead.",
	"connect":          "peerturn connect [--relay URL] [--no-invite] [--reuse] [--lifetime 24h] [--json]\n  From a Claude Code session: initialize once, start the daemon, bind this session and print a coauthor link. --reuse prints the latest unused link again instead of minting one.",
	"daemon start":     "peerturn daemon start [--state-dir DIR]\n  Run the daemon in the foreground (peerturn connect starts it detached).",
	"daemon stop":      "peerturn daemon stop [--json]",
	"daemon status":    "peerturn daemon status [--json]",
	"invite create":    "peerturn invite create --current [--lifetime 24h] [--reuse] [--json]\n  Print a coauthor link for this session; --reuse prints the latest unused link again.",
	"invite list":      "peerturn invite list --current [--json]\n  List this session's unused invitations with their expiry.",
	"invite revoke":    "peerturn invite revoke ID --current [--json]\n  Withdraw an unused invitation.",
	"join":             "peerturn join --current --link-stdin [--json] < link.txt\n  Accept a coauthor link read from stdin. Pasting the link into the session and calling peer_join is the usual way.",
	"peers":            "peerturn peers [--current|--session LABEL] [--json]",
	"send":             "peerturn send --current --peer ID --kind coauthor --stdin [--json] < body.txt",
	"reply":            "peerturn reply --current --message ID --stdin [--json] < body.txt",
	"inbox":            "peerturn inbox --current [--after CURSOR] [--json]",
	"receipt":          "peerturn receipt --current ID [--json]",
	"status":           "peerturn status [--current|--session LABEL] [--json]",
	"doctor":           "peerturn doctor [--current] [--json]\n  Check the daemon, relay, binding, hook, certificate and links; print what to do.",
	"grants list":      "peerturn grants list --current [--json]",
	"grants revoke":    "peerturn grants revoke ID --current [--json]\n  End the link that uses this grant.",
	"mcp":              "peerturn mcp --current\n  Run the stdio MCP proxy for the current session (registered by peerturn setup).",
	"session bind":     "peerturn session bind --current [--json]\n  Bind this Claude Code session to the daemon; normally automatic.",
	"session unbind":   "peerturn session unbind [--current] [--json]\n  Disconnect this session and invalidate its grants.",
	"delivery resolve": "peerturn delivery resolve ID --current --sent|--not-sent [--json]\n  Settle a delivery the daemon could not confirm.",
	"keys isolate":     "peerturn keys isolate [--state-dir DIR] [--json]",
}

func helpRequested(args []string) bool {
	for _, arg := range args[1:] {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

// commandName resolves `grants revoke` and friends the way parseCommand does,
// so help follows the same spelling the parser accepts.
func commandName(args []string) string {
	if len(args) == 0 {
		return ""
	}
	name := args[0]
	if _, ok := commandUsage[name]; ok {
		return name
	}
	if len(args) > 1 && !strings.HasPrefix(args[1], "--") {
		if _, ok := commandUsage[name+" "+args[1]]; ok {
			return name + " " + args[1]
		}
	}
	if name == "grants" {
		return "grants list"
	}
	return name
}

func usageFor(args []string) string {
	name := commandName(args)
	if text, ok := commandUsage[name]; ok {
		return "Usage: " + text + "\n"
	}
	names := []string{}
	for candidate := range commandUsage {
		if strings.HasPrefix(candidate, name+" ") {
			names = append(names, "  "+commandUsage[candidate][:strings.Index(commandUsage[candidate], "\n")+1])
		}
	}
	if len(names) > 0 {
		return "Usage:\n" + strings.Join(names, "")
	}
	return usage
}

func writeUsage(stdout io.Writer, args []string) int {
	if _, err := io.WriteString(stdout, usageFor(args)); err != nil {
		return 1
	}
	return 0
}

// withUsage turns a bare invalid_arguments into an answer: the exact syntax.
func withUsage(err error, args []string) error {
	public := localipc.PublicError(err)
	if public.Code != "invalid_arguments" {
		return err
	}
	text := strings.TrimSuffix(usageFor(args), "\n")
	return localipc.Explain(public.Code, public.State, strings.ReplaceAll(text, "\n", " "))
}
