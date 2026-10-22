# M6 Claude profile and product refusal

Historical preparation run. A later owner decision on 22 October 2026 removed
the monetary prerequisite in favour of receiver quotas. The separate
[M6b qualification](m6b/README.md) records the new implementation and live runs.
The original verdict and observations below are preserved as evidence of this run.

22 October 2026. Disposable Linux ARM64 containers `a1` and `a3`, Claude Code
**2.1.306**, fixture gateway model `deepseek-flash[1m]`. Only synthetic lab
conversations, files and capabilities were used. No host model or real Keychain
was accessed. [Keystore acceptance](keystore/README.md) is a separate `a2` run.

**Verdict: product `ask` remains unavailable.** The custom provider's billed
monetary ceiling is unqualified. Native `--max-budget-usd 1` accounting does not
establish that ceiling; persisted deadline/output/concurrency/hour limits do not
replace it. The final daemon returns an authenticated reverse-channel refusal
with `stage=rejected`, `outcome=responder_unavailable`, and
`reason_code=budget_unavailable`. A useful signed responder answer was not
accepted. Codex native responder acceptance was not performed in this run.

## Evidence

| Scenario | Observation | Evidence |
| --- | --- | --- |
| Previous bare Read allow | Outside file and its in-project symlink alias are readable. This is a positive leak control. | [01a](01a-bare-allow.json) |
| Root-scoped Read allow, permission prompts none | Public project Read succeeds; outside Read, symlink Read, Grep and Glob return errors; outside canary absent. | [01b](01b-scoped-allow.json) |
| Root scope plus native restricted mode | The same outside paths/searches fail; ordinary project Read succeeds; effect file absent. Read/Grep/Glob are the only advertised tools. | [01c](01c-restricted-scoped.json) |
| Native committed prefix | A resumed fork at `last-prompt.leafUuid` retains the previous context marker, exact native system prompt, requested model and effort. Parent transcript SHA256 is unchanged. | [02](02-native-prefix.json) |
| Same-process startup check | Trusted probe completes before peer stdin is written. Both per-turn init events report exactly Read/Grep/Glob and empty MCP/plugins/skills. | [02](02-native-prefix.json) |
| Initial real Go runner diagnostic | Refuses with `responder_failed`, returns no answer, no state/outside canary and no effect file. This is a failure, not reply acceptance. | [03](03-runner-initial-refusal.json) |
| Final actual native peers | Two real native Claude bindings, Secret Service-backed receiver, signed refusal with `budget_unavailable`; no responder reservation, new native journal or model-turn-budget charge. | [04](04-final-product-refusal.json) |
| Live Codex receiver negative | **NOT RERUN** in M6. `TestCodexAskGetsSignedUnavailableWithoutLiveDelivery` covers the signed refusal with a native-runtime fixture; historical M5 controls are separate. | [Unit regression](../../../../internal/daemon/responder_rejection_test.go), [M5](../m5-ask/README.md) |

[Scope metadata](01-read-scope.json) and [native record metadata](01-parent-record-metadata.json)
retain the version and sanitized observations. Native UUIDs are replaced with a
fixture placeholder. Raw conversations, invitation fragments, environment values
and key material were not exported.

The initial runner artifact predates the deterministic synthetic-assistant-error
regression fix. Its `model_input` entry is not accepted as evidence of provider
consumption. The final code rejects native synthetic API-error assistant frames
before that stage; the new regression first failed, then passed. The failed
native diagnostic was preserved and was not repeated to force a positive result.

## Final product artifact

Final uncommitted worktree source: branch `feat/m6-claude-ask`, base `b8072d5`.
Client and relay executable SHA256:

```text
11edf3d36452e6c8d3569cf409215d9cf918b9c29c91c5fce406aad9e13bc8e3
```

The final product test used ordinary interactive Claude sessions in the container user's `$HOME/work`
with the actual PeerTurn plugin. Native Bash children performed `session bind
--current`, invite creation, join, peer listing and the outbound `ask`. The plugin
received a real native nonce handshake; a preview label was not counted as native
binding evidence. Both owners retained normal Bash availability and
`bypassPermissions`.

The receiver wrapping key was rotated through the real Secret Service and
reloaded by a restarted daemon. Status reported
`authority_secrets_isolated=true`, `fork_responder=false`, and
`responder_reason=budget_unavailable`. The original file wrapping key was absent.
The one-use fixture invitation was sent only in a private container-to-container
HTTP request body and then piped into the joining native tool command; it was not
printed or placed in a command argument.

Updating the daemon required native plugin reactivation before a fresh handshake
in the same original TUI UUID. [The first observation](04-initial-bootstrap-refusal.json)
includes that explicit owner rebind inside its measurement window, so its whole
transcript comparison is false. That artifact is not used to claim an unchanged
transcript. After the receiver was idle and confirmed bound on the final daemon,
a new independent message was sent using a fresh measurement window. The final
artifact records the full transcript comparison, native-journal/assistant counts,
absence of the ask body in owner context, reservation count and model-turn budget.
Each send used a new message ID; no ambiguous native submission was retried.

## Reproduction

Use only fresh disposable machines with the exact assigned names. The profile
fixture refuses a different hostname, an existing fixture directory or unexpected
managed files. It creates only fake canaries. The following are own-source
fixtures, not research dependencies:

1. Copy and run [claude_profile_probe.py](claude_profile_probe.py) inside `a1`.
   It creates one synthetic parent and three resumed forks, saves raw output
   inside the container, and exports canary booleans and tool results only.
2. Run [claude_protocol_probe.py](claude_protocol_probe.py) inside the same `a1`.
   It uses a clean allowlisted environment and explicitly disables the three
   pinned built-in plugins. The source includes the exact native arguments and
   stdin protocol. An earlier diagnostic inherited a container effort override;
   the exported successful control uses the production-style clean environment.
3. The [Go runner fixture](runner/main.go) is a native-profile diagnostic. Its
   authority callback is deliberately synthetic and is labeled as such in output;
   it does not qualify an authority backend or product admission.
4. [live_refusal.py](live_refusal.py) records the actual product orchestration:
   `setup`, `bootstrap`, `invite`, `transfer`, `peers`, `final-client`,
   `start-services`, native `/reload-plugins` when necessary, `bootstrap`,
   `freeze`, `submit`, then `observe`. Wait for each native command's saved result
   and a confirmed idle binding before the next dependent phase. Never export the
   native logs or fixture invitation file. The local artifact server serves only
   the own-source plugin archive and native diagnostic binary.

The lab client from the base checkout was used because this worktree has no
broker token file. Only `a1` and `a3` were operated by this fixture. The keyring
service uses `gnome-keyring-daemon --login --foreground`, then `--start`, in one
`dbus-run-session`; its complete working recipe is in the keystore evidence.
The TLS relay used a one-day fixture certificate for `a3`, verified by both clients.

## Checks and limits

Targeted checks ran with isolated Go caches:

```sh
go test -race ./internal/claude -count=1
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 \
  run ./internal/claude ./research/lab/results/m6-ask/runner
```

Both passed. A live ordinary TUI then exposed the pinned unscoped
`file-history-snapshot` metadata record; its narrow regression first failed and
passed after recognizing only that nonconversation record, while retaining the
complete prefix hash. The targeted snapshot race checks passed after that change.
The integrator reports a final successful full `make ci` and independent
review with no critical findings.

No macOS managed-preferences proof, provider billing proof, arbitrary busy-session
leaf ancestry proof or universal native context-slot equivalence is claimed.
Read path rules do not defeat in-project hardlinks. The full history may contain
noncore secrets, and redaction catches only documented exact encodings and
heuristics. [The implementation profile](../../../../docs/m6-claude-profile.md)
states these boundaries. `ask` stays closed; no fallback sends it to the live
receiver. [Teardown](teardown.json) confirms that `a1` and `a3` were removed after
evidence export and the fixture-only HTTP server was stopped.
