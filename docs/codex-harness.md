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

Do not deploy this migration before verifying both live model routes and
preparing the volume. Build success and catalog discovery are not evidence of
live Responses/tool-call compatibility or account access.

- `CODEX_SIMPLE_MODEL`: required; selected value
  `deepseek/deepseek-v4.1-flash-1017`.
- `CODEX_COMPLEX_MODEL`: required; selected value `openai/gpt-6-luna`.
- `OPENROUTER_API_KEY`: required server-side credential from the funded
  OpenRouter account. **Both models use this account/balance**, including Luna.
  No direct OpenAI credit or desktop login/ChatGPT subscription is used.
  Do not paste keys into Slack or commit them.
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
Keep `OPENROUTER_API_KEY`. Legacy `OPENROUTER_MODEL`/routing variables,
`CODEX_MODEL`, `CODEX_API_KEY`, `OPENAI_*`, `OPENCODE_*` and `DEEPSEEK_*` no longer
select the assistant and can be removed separately after rollout.

The provider is configured for `https://openrouter.ai/api/v1`, Responses wire
format, HTTP streaming and explicit key authentication. The same-origin
`https://openrouter.ai/api/v1/models` catalog is configured explicitly because
environment-key authentication alone does not enable Codex catalog discovery.
The unauthenticated public list returned a `data` array during development;
that is **not** proof of a Codex `models` catalog. Before rollout, validate the
authenticated catalog's schema and entries against the pinned client. If the
provider still returns only its ordinary API model list, obtain a Codex-compatible
catalog rather than accepting generic model metadata. This remains a live-test
gate, not something the offline transport test proves.
No shell-based key helper is used. The adapter only accepts HTTPS provider URLs
(loopback HTTP is permitted for offline tests); a catalog cannot send the key
to a different origin.

## Model routing owned by Codex

Standalone common greetings and thanks are answered locally without model calls
(see conversations.md); they do not enter the native thread. Every other user
request starts on DeepSeek. Its instructions keep simple questions, RSS
counts/titles and straightforward summaries on that model.
For writing/debugging code, architecture or complex multi-step reasoning, Codex
is instructed to call the internal `handoff_to_luna` tool before attempting the
task. The tool takes no model identifier or other arguments; Go does not classify
the user's intent. This is model-guided routing, not a perfect complexity oracle.

The adapter acknowledges the tool, interrupts the current native turn, waits for
both the interrupt acknowledgement and turn termination (at most 10 seconds),
then starts Luna in **the same native thread**. Original messages and tool
results remain in Codex history. Only Luna's final answer is delivered. A race
where the old turn completes normally is safe; failed turns/interrupts are not
treated as a handoff. New data calls are refused while switching, and only one
handoff is permitted per user run. The next user message starts on DeepSeek again.
Both stages share one concurrency slot and the original 120-second deadline.

Complex tasks incur a short DeepSeek routing call **plus** Luna's model/tool
usage; they are not billed as Luna-only. Failed/canceled upstream requests may
also incur usage. There is no automatic free-model fallback or hidden direct
OpenAI route. A paid model failure uses the normal handled-error path.
This integration supports coding assistance in replies, not arbitrary code
execution or repository editing: filesystem/shell tools remain disabled. Those
capabilities require separately designed tenant isolation and authorization.

## Isolation and tools

One child handles one user run (one or two sequential model turns). Each workspace/channel/thread/user tuple has
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
deduplicated by call ID within the run. Default data tools are read-only. The
optional [repository task tools](repository-tasks.md) connect GitHub and persist
user-bound task drafts; they cannot edit repositories or execute code. Repository
writes require isolated workers, durable idempotency and explicit authorization,
not merely a new schema.
Bounds: four concurrent children, 120 seconds per run (including slot wait),
45 seconds per tool, 256 tool calls, 2 MiB tool result and 35,000 final characters.
These are resource limits, not restrictions on which RSS entries can be read.

## History, completion and failures

Postgres keeps the delivered transcript and the native `agent_thread_id` in the
existing conversation JSON. Existing transcripts bootstrap the first Codex
thread. Subsequent turns use `thread/resume`; Codex manages full native history.
Scopes include Slack workspace, channel, thread AND user, not just channel.

Only a matching final `turn/completed` with status `completed` and nonempty final
output is success. Commentary, deltas, EOF and failed/interrupted turns cannot
be mistaken for a final response. An acknowledged routing interruption only
permits the next model turn; it is never a successful user reply. Tool results can fail within a successful turn;
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
Codex token counters, when reported, feed the dashboard separately for each
model stage, subtracting the previous native-thread total to avoid double
counting. Missing counters are unknown, not zero-cost proof. OpenRouter's usage
ledger remains the billing authority; these counters are not a dollar invoice. Native rollout
files and the existing Postgres analytics/transcripts contain private content;
restrict volume/database access and plan retention before broader use.

## Verification

Run `go test -race ./...`, `go vet ./...` and the Linux build. Automated tests
mock model traffic and cover protocol completion, tools, isolation and failures.
The pinned native binary can be handshaken and a thread started without starting
a model turn. This validates protocol shape only, not inference/account access.
For a full offline native tool/result/final/resume roundtrip, set
`NUDGY_TEST_CODEX_BINARY` to the pinned binary and run
`go test ./internal/codex -run TestNativeAppServer -count=3`.
This tests data-tool roundtrips, native DeepSeek-to-Luna model overrides,
per-model usage accounting and return to DeepSeek after a subprocess restart.
It uses a local fake Responses server, not a paid model. It needs permission
to bind a loopback port. Also build the Docker image when a Docker engine is
available; compiling the Linux Go binary alone does not validate the image.

Before rollout, obtain approval for bounded paid smoke tests with both selected
OpenRouter model IDs: verify Responses streaming, dynamic tool output roundtrip,
same-thread cross-model history compatibility, actual model identity, latency
and OpenRouter billing. Offline tests do not establish these live properties.

After those checks, key/volume setup and the user's push/deploy, test greetings,
thanks, RSS counts/categories, complete paginated titles, content-backed summary,
follow-up selection, optional web research, simultaneous users and restart/resume.
Verify the reaction, native IDs, real sources, timeouts, billing and dashboard.

Protocol reference: https://learn.chatgpt.com/docs/app-server

OpenRouter integration: https://openrouter.ai/blog/tutorials/codex-cli-openrouter/

### Live smoke-test findings (2026-10-24)

With explicit approval, both selected OpenRouter models completed streamed
Responses requests and a synthetic function-call/output/final-answer loop.
The pinned native Codex binary then used the real Runtime adapter to:

- retrieve a synthetic RSS count through `rss_inventory` and answer correctly;
- restart/resume the same persisted thread and answer a short thanks message;
- route a coding request from DeepSeek to Luna in that thread and return code.

The direct checks used four model requests (reported cost $0.00019050). The
native adapter checks used five (reported cost $0.00143129). Total reported
generation cost: $0.00162179. These are observations from tiny synthetic tests,
not per-user pricing estimates. No real Slack messages, RSS accounts, Railway
deployment or production traffic were used to establish these results.

**Deployment gate still open:** the authenticated `/models` response returned
the ordinary `data` list, not a Codex `models` catalog containing the selected
slugs. This also occurred with native Codex request headers. Functional turns
worked with fallback metadata; that does not verify context limits, compaction
or the complete model capability metadata. Fix and validate the catalog before
deployment. The successful smoke test alone must not be treated as rollout
approval.

The committed `TestLiveOpenRouterHarness` is disabled by default. Only after
explicit approval, place the dedicated test key in Git-ignored `.env.local`,
set `NUDGY_LIVE_OPENROUTER_TEST=1` and `NUDGY_TEST_CODEX_BINARY` to the pinned
binary, and run just that test with `-count=1`. It sends only synthetic inputs
through a loopback proxy enforcing eight requests maximum, 32 KiB request bodies,
1024 output tokens per request and a model allowlist. The test reports model
identity, usage/cost and the separate `catalog_compatible` deployment gate;
it never prints the key. Regular test suites do not enable paid calls.
