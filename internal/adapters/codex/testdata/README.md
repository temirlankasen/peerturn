# Codex queue contract fixture

`codex-0.163.0-owner-queue-list.json` is a synthetic, source-derived response
shape, not a live capture or proof of owner provenance. Its queue item ID, text
and UUID are invented. It models an owner-created `codex queue` item returned
by `thread/queue/list`; owner IDs need no `peerturn:` prefix.

All references pin OpenAI Codex commit
`83086188ae0614bd882818ff1aa23f3194962012`:

- [`session_queue_commands.rs:52,116–123`](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/tui/src/session_queue_commands.rs#L52):
  `codex queue` generates a UUIDv7 and passes its string as
  `ThreadQueueAddParams.client_user_message_id`.
- [`thread.rs:926–966`](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server-protocol/src/protocol/v2/thread.rs#L926):
  `QueuedSubmission.client_user_message_id` is a required `String`;
  `ThreadQueueListResponse.data` holds those items and `next_cursor` is nullable.
- [`thread_queue_processor.rs:121–130,340–350`](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/app-server/src/request_processors/thread_queue_processor.rs#L340):
  list maps every core item through `api_queued_submission`. Although core
  `TurnInput::UserInput.client_id` is optional, `None` returns an internal error;
  it does not produce a successful queue item with a null ID.

TUI pending input is a separate local queue:

- [`input_queue.rs:28`](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/tui/src/chatwidget/input_queue.rs#L28)
  stores `queued_user_messages` in a `VecDeque`.
- [`input_flow.rs:237–269`](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/tui/src/chatwidget/input_flow.rs#L237)
  drains local queued input into the normal submission path.
- [`input_submission.rs:436–454`](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/tui/src/chatwidget/input_submission.rs#L436)
  creates a UUIDv4 on submission; [`app_server_session.rs:1457`](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/tui/src/app_server_session.rs#L1457)
  sends `Some(client_user_message_id)` in `TurnStartParams`.

The regression test accepts the source-derived owner UUID shape and rejects
missing, null and non-string client IDs before any peer submission. The pinned
source does not justify accepting null IDs. This fixture establishes structural
compatibility only, not queue consumption, turn completion or native origin.
