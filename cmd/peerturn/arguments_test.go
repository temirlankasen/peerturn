package main

import "testing"

func TestParseCommandBareGrantsListsGrants(t *testing.T) {
	for _, args := range [][]string{{"grants", "--current"}, {"grants"}, {"grants", "list", "--current"}} {
		command, err := parseCommand(args)
		if err != nil || command.name != "grants list" {
			t.Fatalf("%v: name=%q err=%v", args, command.name, err)
		}
	}
	if _, err := parseCommand([]string{"grants", "unknown"}); err == nil {
		t.Fatal("unknown grants subcommand accepted")
	}
}
