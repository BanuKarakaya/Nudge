# Self-hosted Codex harness

Nudgy uses a pinned Codex **app-server** subprocess over newline-delimited JSON
RPC on stdin/stdout. It does not call `codex exec`, the managed Agents API, or a
chat-completions model router. There is no legacy-model fallback.

Codex owns understanding, planning, tool selection, the model/tool loop, native
conversation context/compaction, and final generation. Go owns Slack signature
verification and delivery, identity, permissions, tool execution, persistence,
bounded execution, scheduling/outbox delivery and telemetry. These infrastructure
responsibilities are not delegated to the model.

## Workspace credential routing (current)

Production uses `codex.TenantRuntime`: authenticated user/workspace identity
selects encrypted provider credentials and model IDs saved at `/dashboard/ai`.
Global OpenRouter/OpenAI/Gemini keys and model environment variables are not a
production fallback. Scheduled digests and independent merge-intent calls use
the same tenant policy. See [workspace panel](workspace-panel.md) for the current
configuration and migration requirements. The environment-based examples below
refer to the original rollout and opt-in developer smoke tests, not customer
configuration. Persistent state directories and the pinned transport still apply.

## Current configuration and deployment

Use [the self-hosting guide](self-hosting.md) for a new installation. Managers
configure their own OpenAI or OpenRouter key, daily model and optional advanced
model in `/dashboard/ai`. The production runtime does not read server-wide model
keys or model IDs. Optional Google search is also configured per workspace.

- `NUDGY_CODEX_STATE_DIR`: persistent absolute path, default
  `/var/lib/nudgy/codex`. The container runs as uid/gid **1000**; the directory
  must be writable by that user. Do not mount a developer's personal Codex home.
- `CODEX_BINARY`: defaults to `codex`; an absolute local path is supported.
- AI credential encryption uses `NUDGY_AI_ENCRYPTION_KEY`, falling back to
  `FEEDBIN_ENCRYPTION_KEY` only as documented. Keep that choice stable.

The Dockerfile pins `@openai/codex` **0.164.0**. Review protocol compatibility
before changing it. The Node launcher and native subprocess are terminated as a
process group on timeout. No app-server TCP/WebSocket port is exposed.

Use one application replica. The durable [Slack queue](slack-jobs.md) does not
make OAuth in-memory state or separate native-state volumes interchangeable.
Back up the database, encryption keys and runtime volume; stop old instances
before a rollout. Railway is optional.

The adapter uses the Responses protocol with streaming and explicit key
authentication. A successful provider catalog fetch is not proof of inference,
model account access, complete capability metadata or tool-call compatibility.
Validate the selected models with a deliberate live acceptance test before
serving users. Offline protocol tests do not prove that gate.

## Model routing owned by Codex

Standalone greetings and thanks can be answered locally without model calls.
Other requests start on the workspace's daily model. When a different advanced
model is configured, the model can request a handoff for complex work. The
internal tool retains its historical name `handoff_to_luna`; that name does not
force a particular vendor or model. It uses the manager's advanced-model choice.

The adapter acknowledges the handoff, interrupts the current turn, waits for
acknowledgement and termination, then starts the advanced model in the same
native thread. Original messages and tool results remain in context. Only one
handoff is permitted per user run, and both stages share the original deadline
and concurrency slot. A new user request starts on the daily model again.

A two-stage request can incur usage on both models. Failed or canceled provider
requests may also incur usage. There is no hidden shared account or free-model
fallback. Repository editing is implemented through Go permission-checked tools;
native filesystem and shell execution remain disabled.

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
optional [repository editing tools](repository-tasks.md) clone GitHub source,
read/write a user-bound encrypted workspace, freeze it and automatically publish
a new branch in the same repository when repository_edit_finish succeeds. Users
clone/fetch that branch for manual testing; no extra push approval is requested.
The host fixes the target branch and rechecks owner identity and write permission.
GitLab.com exposes the same workflow through namespaced `gitlab_*` tools and a separate connection/project selection ([setup](gitlab.md)). Provider choice is explicit; GitHub and GitLab credentials/tasks cannot be substituted.
Repository scripts/tests and native shell tools remain disabled. Separate PR/MR
creation, status and explicitly authorized merge tools are available as described
below. Pending edits survive restarts and can be resumed.
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
The 23:00 scheduled RSS notification sends titles and links directly, without
using this runtime. The legacy AI test-digest handler (not mounted by `cmd/server`) uses the digest
generator; a conversational explanation is not successful generation:
at least one RSS retrieval must succeed and no RSS retrieval may have failed.
Otherwise generation fails instead of treating a retrieval error as an empty inventory.
Stable partial-answer items are joined in order, deduplicated by native item ID.

The thinking reaction is managed by the Slack transport, before waiting/generation
and removed after delivery or on handled failure. On generation failure,
the native thread pointer is invalidated so a later run bootstraps only delivered
history. Delivery retries use the saved response checkpoint. Native conversation directories are user-scoped and cleaned after `NUDGY_RETENTION_DAYS` of inactivity. Personal deletion also removes them. If `thread/resume` rejects a missing rollout (`-32600`, known
`no rollout found for thread id` response), the adapter starts one replacement
thread in the same request, with the configured model and current tools. It
bootstraps only the delivered transcript from Postgres and resets native token
counters. Previous tool calls are not replayed. Recovery is limited to this
pre-turn failure: invalid parameters, provider mismatches, transport errors and
failed/uncertain running turns do not trigger an automatic restart. Logs contain
only a recovery reason, never raw provider diagnostics. A volume is still needed
to retain full native context; recovery retains only the bounded delivered history.

Jira/Trello management tools use the `management-v2` native tool version, so
threads created with the old read/create/move toolset bootstrap once with member
assignment, comments and the other new tools. The Slack thread stays the same;
its delivered history is retained. Bump the integration tool version when its
persisted tool surface changes.

Acknowledged Slack mentions are persisted in the [PostgreSQL job queue](slack-jobs.md).
Interrupted generation restarts from delivered history after claim recovery;
persisted responses retry delivery without regenerating. A delivery failure keeps
the next native thread pointer in that response checkpoint until delivery and
history finalization succeed. Scheduled notifications retain their separate outbox.
Slack and database commits are not atomic, so there is no exactly-once guarantee.

Railway logs contain request stages and native thread/turn IDs, tool names,
durations and outcomes, not provider stderr, arguments, content or API keys.
Codex token counters, when reported, feed the dashboard separately for each
model stage, subtracting the previous native-thread total to avoid double
counting. Missing counters are unknown, not zero-cost proof. The selected provider's usage
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

### Historical live smoke-test findings (2026-10-24)

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

## PR/MR tools

[PR/MR creation, status and explicit merge](change-requests.md) are separate
user-bound tools. The host verifies the original Slack merge command and pins the
provider-observed head SHA in PostgreSQL before mutation. Tool schemas never
accept an authorization boolean, alternate user, or replacement request text.
Native tool versions change to rebuild existing threads with the new schemas
while preserving delivered history. GitLab OAuth now requests `api`; existing
connections without that scope can still edit but must reconnect for MR tools.

## Tool-specific routing

Workspace AI settings can map exact tool names to allowed provider model IDs.
The host interrupts before the first matching tool executes, resumes the same
conversation with the selected model, and permits that call to be retried there.
Routing happens at most once per request; it does not bypass the approval wrapper.
See [operations](operations.md) for configuration and evaluation limitations.
