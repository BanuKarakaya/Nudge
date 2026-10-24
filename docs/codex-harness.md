# Self-hosted Codex harness

Nudgy uses a pinned Codex **app-server** subprocess over newline-delimited JSON
RPC on stdin/stdout. It does not call `codex exec`, the managed Agents API, or a
chat-completions model router. There is no legacy-model fallback.

Codex owns understanding, planning, tool selection, the model/tool loop, native
conversation context/compaction, and final generation. Go owns Slack signature
verification and delivery, identity, permissions, tool execution, persistence,
bounded execution, scheduling/outbox delivery and telemetry. These infrastructure
responsibilities are not delegated to the model.

## Configuration and deployment gate

Do not deploy this migration before selecting a model and preparing the volume.
Build success is not evidence of live model access.

- `CODEX_MODEL`: required, no default. Select a model available to the configured
  API account and compatible with Responses/tool calls. No model was chosen by
  this migration. Changing this setting changes future runs; there is no model
  escalation or automatic free fallback.
- `CODEX_API_KEY`: required dedicated server-side OpenAI API credential. API
  inference is separately billed; no desktop login or ChatGPT subscription is
  copied. Do not paste keys into Slack or commit them.
- `NUDGY_CODEX_STATE_DIR`: absolute path, default `/var/lib/nudgy/codex`. Mount a
  persistent Railway volume here. The image runs as uid/gid **1000**; ensure the
  mounted directory is owned/writable by that user. Never mount an existing
  developer Codex home. Back up this directory alongside Postgres.
- `CODEX_BINARY`: defaults to `codex`; optional absolute local path.
- `GEMINI_API_KEY` / `GEMINI_MODEL`: optional existing public web-search tool,
  not a second harness or fallback answer generator.

The Dockerfile pins `@openai/codex` **0.164.0**. Dynamic tools are experimental:
review/regenerate schemas and run protocol tests before changing the pin. The
Node launcher and native subprocess are both terminated as a process group on
timeout. No app-server TCP/WebSocket port is exposed.

For the initial Railway rollout use **one application replica**. The existing
Slack event deduplication and conversation locks are process-local. A shared
volume does not provide distributed locking. Stop old instances before rollout.
Keep existing database, Feedbin encryption, Slack, account-link and admin secrets.
Legacy `OPENROUTER_*`, `OPENAI_MODEL`, `OPENCODE_*` and `DEEPSEEK_*` variables no
longer select the assistant and can be removed separately after rollout.

## Isolation and tools

One child handles one active turn. Each workspace/channel/thread/user tuple has
a hashed, separate Codex state directory. Only PATH, that dedicated Codex home,
the model credential and a logging setting enter its environment: no database,
Slack, Feedbin, Raindrop or administrator credentials. Native environment access
is disabled with `environments: []`; shell, apps/plugins, browser/computer tools
and subagents are disabled. The process runs non-root. Unexpected approval or
other server requests are denied, never automatically approved.
Native image/file viewing, goals and hooks are also disabled. The pinned-binary
test checks the complete advertised tool surface; the remaining native
`request_user_input` control request is refused (questions belong in Slack final
answers). These configuration restrictions are not an OS-level tenant sandbox:
all children run under the application UID. Do not expose this adapter as a
general-purpose code-execution service.

The application binds tools to the authenticated Slack user. Tool arguments
cannot select a different user. RSS inventory, paginated articles/content and
bookmark lists return source data, not a second AI's summary. Tool errors do not
mean an empty inventory. Articles are untrusted source material. Optional public
web research uses the existing grounding provider and returns source links;
Codex decides when to request it and writes the final Slack answer.

Tool calls are checked against both active native thread and turn IDs. Calls are
deduplicated by call ID within the run. Tools are read-only; adding writes later
requires durable idempotency and explicit authorization, not merely a new schema.
Bounds: four concurrent children, 120 seconds per run (including slot wait),
45 seconds per tool, 256 tool calls, 2 MiB tool result and 35,000 final characters.
These are resource limits, not restrictions on which RSS entries can be read.

## History, completion and failures

Postgres keeps the delivered transcript and the native `agent_thread_id` in the
existing conversation JSON. Existing transcripts bootstrap the first Codex
thread. Subsequent turns use `thread/resume`; Codex manages full native history.
Scopes include Slack workspace, channel, thread AND user, not just channel.

Only a matching `turn/completed` with status `completed` and nonempty final
output is success. Commentary, deltas, EOF and failed/interrupted turns cannot
be mistaken for a final response. Tool results can fail within a successful turn;
Codex must explain that rather than fabricate data.
For scheduled digests, a conversational explanation is not successful delivery:
at least one RSS retrieval must succeed and no RSS retrieval may have failed.
Otherwise generation fails before enqueue, preserving the scheduler's retry.
Stable partial-answer items are joined in order, deduplicated by native item ID.

The thinking reaction is managed by the Slack transport, before waiting/generation
and removed after delivery or on handled failure. On generation/delivery failure,
the native thread pointer is invalidated so a later run bootstraps only delivered
history. Old native files remain for recovery; there is no automated retention
policy. Missing volume/session files produce an explicit failed turn, not a
silent switch to a different model. A subsequent attempt creates a new thread
after invalidation.

This does not make acknowledged Slack requests durable across a hard process
crash. An in-flight request/reaction may still require retry/manual cleanup after
a restart. Pending scheduled-message deliveries retain the existing durable
outbox, but pre-enqueue generation is not durable. No exactly-once guarantee.

Railway logs contain request stages and native thread/turn IDs, tool names,
durations and outcomes, not provider stderr, arguments, content or API keys.
Codex token counters, when reported, feed the dashboard as an aggregate turn
usage record; missing counters are unknown, not zero-cost proof. Native rollout
files and the existing Postgres analytics/transcripts contain private content;
restrict volume/database access and plan retention before broader use.

## Verification

Run `go test -race ./...`, `go vet ./...` and the Linux build. Automated tests
mock model traffic and cover protocol completion, tools, isolation and failures.
The pinned native binary can be handshaken and a thread started without starting
a model turn. This validates protocol shape only, not inference/account access.
For a full offline native tool/result/final/resume roundtrip, set
`NUDGY_TEST_CODEX_BINARY` to the pinned binary and run
`go test ./internal/codex -run TestNativeAppServerToolRoundTrip -count=3`.
This uses a local fake Responses server, not a paid model. It needs permission
to bind a loopback port. Also build the Docker image when a Docker engine is
available; compiling the Linux Go binary alone does not validate the image.

After model selection, key/volume setup and the user's push/deploy, test greetings,
thanks, RSS counts/categories, complete paginated titles, content-backed summary,
follow-up selection, optional web research, simultaneous users and restart/resume.
Verify the reaction, native IDs, real sources, timeouts, billing and dashboard.

Protocol reference: https://learn.chatgpt.com/docs/app-server
