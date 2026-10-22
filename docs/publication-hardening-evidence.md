# Publication hardening evidence

This record covers the MCP framing and Codex responder changes made after the
publication review of `660ebb2`. It does not qualify restricted ask, a release,
native join delivery or a complete isolation boundary.

## MCP framing

The initial `TestMCPJoinEscapingAndOversizeDoNotTerminateProxy` run exited 1:
valid escaped text and oversized requests terminated the proxy with `too_large`.
The frame allowance now covers six encoded bytes per invitation input byte plus
16 KiB for the RPC envelope. Larger frames are drained with bounded storage and
receive JSON-RPC `-32602`; later requests remain usable.

The regression covers the 64 KiB escaped-byte limit, escaped input over that
limit, multibyte UTF-8 over the byte limit and a frame larger than the allowance.
Each rejected case checks a later successful ping and absence of a join call to
the daemon. The targeted MCP package race suite exited 0.

## Codex user configuration

The installed `codex --version` reported `codex-cli 0.163.0`. Its
`codex exec --help` exposes `--ignore-user-config`, which skips
`$CODEX_HOME/config.toml` while retaining authentication from `CODEX_HOME`.
The [official CLI reference](https://learn.chatgpt.com/docs/developer-commands?surface=cli)
documents the same behavior. The closed responder now passes that flag before
`fork`, excluding MCP servers from that user configuration.

The command regression first exited 1 because the flag was absent. The updated
Codex adapter race suite exited 0. This is command-construction evidence;
project/plugin integrations, authority-file access and effective native fork
isolation still need qualification. Product ask remains unavailable.

## Invitation and public documentation

The relay page already omitted installer overrides. Its regression now also
rejects laboratory mode, source-override and key-override options; the relay race
suite exited 0. Those options are documented only in the
[developer installer lab](install-lab.md), separate from the recipient guide.

Public documents use the repository's Make targets and omit private development
tool names, private worktree layout, personal note-service bookkeeping and the
local timezone. Excluded raw laboratory records retain their historical data.

Targeted pinned lint for MCP, the Codex adapter and relay exited 0 with no issues.
Content checks over the previous 529-file public snapshot and those changes exited 0:
local Markdown links, JSON syntax, 400-line file limits and the instruction-file
byte limit passed. The complete gate and independent review are separate
integrator responsibilities.

The publication fix3 snapshot contains 532 public files, including three new
invitation-origin/cache test and implementation files. Export metadata is UTC;
the exporter now verifies the filtered source tree hash before reporting success.
