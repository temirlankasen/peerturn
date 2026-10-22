# Prior art

PeerTurn builds on established agent messaging, local session bridges, and
encrypted store-and-forward systems. These references informed the design; they
are separate projects, not PeerTurn dependencies or security endorsements.
Links were checked on 22 October 2026. Upstream capabilities can change.

| Project | Relevant idea | PeerTurn design question |
| --- | --- | --- |
| [agent-talk](https://github.com/xhluca/agent-talk) | Agent-facing skills and invite workflows over retalk. | How does a colleague connect from an existing coding session? |
| [retalk](https://github.com/xhluca/retalk) | Encrypted CLI messaging, contact identity, and a self-hostable relay. | How do identity, ciphertext custody, and downstream delivery stay distinct? |
| [Relay](https://github.com/AgentWorkforce/relay) / [Relaycast](https://github.com/AgentWorkforce/relaycast) | Agent communication infrastructure, message storage, routing, and subscriptions. | What does transport acceptance prove about a live agent turn? |
| [hcom](https://github.com/aannoo/hcom) | Messaging, observation, and agent lifecycle across terminals and devices. | Where should device/group trust stop and exact session authority begin? |
| [Open Cross-session](https://github.com/leeguooooo/open-cross-session) | Native session messaging and opt-in LAN pairing. | How are live targets, busy input, and peer trust represented? |
| [claude-peers-mcp](https://github.com/louislva/claude-peers-mcp) | Ad hoc discovery and messaging among Claude Code sessions. | Why must discovery remain separate from permission to send? |
| [MCP Agent Mail](https://github.com/Dicklesworthstone/mcp_agent_mail) | Asynchronous inboxes, searchable threads, identities, and advisory file reservations. | How can durable coordination work without assuming an idle model is awake? |
| [agmsg](https://github.com/fujibee/agmsg) | Cross-vendor CLI-agent messaging with a small shell/SQLite footprint. | How small can a bridge remain while making delivery stages explicit? |

Our synthesis is that transport, native model input, and action authority need
separate contracts. Discovery is not a recipient-issued grant; a stored message
is not model input; a reply is not proof that effects happened exactly once.
These are PeerTurn design conclusions, not a claim that every upstream project
promises the same boundaries.

The version-pinned [laboratory summaries](../research/lab/README.md) record
selected historical live probes, including negative and ambiguous results.
They preserve scope and limitations; private raw transcripts, deployment data,
third-party snapshots, and internal research journals are not published.

PeerTurn's current behavior and remaining work are described in the
[README](../README.md) and [specification](spec/requirements.md). `coauthor`
remains strong trust with the receiving agent's normal capabilities. A safe
restricted responder requires more than an encrypted transport or an agent
prompt asking it to be careful.
