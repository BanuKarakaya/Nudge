# Nudgy conversations

App mentions are handled by the self-hosted Codex app-server harness, except
standalone common greetings, how-are-you questions and thanks. These use a
conservative whole-message allowlist and local replies: zero model calls/tokens.
Mixed requests (e.g. “teşekkürler, RSS özetimi ver”) still reach Codex.
Local exchanges appear in interaction analytics as `SOCIAL_REPLY`, with no AI
usage. They are excluded from model-bound history and leave native thread IDs
and usage counters unchanged. Slack authentication, deduplication, reactions,
conversation locking and safe history loading still apply.
See [Codex setup, architecture and deployment gate](codex-harness.md).

Optional [GitHub connection and repository task drafts](repository-tasks.md)
are disabled by default. When configured, authenticated Slack users can connect
GitHub privately, list allowed repositories and save/inspect/cancel task drafts.
Repository editing, test execution and PR publication are not implemented yet.

Go no longer selects intent/action enums, routes free/complex models, repairs
model JSON, or retries summaries through other models. Apart from the local
courtesy replies above, Codex calls user-bound data tools and writes the final reply. It also
creates scheduled RSS summaries; Go still owns schedules and reliable delivery.

Conversation transcripts and native thread IDs are stored in Postgres, keyed by
workspace, channel, Slack thread and user. Native Codex history lives on the
persistent application volume. Missing credentials/model fail startup; no old
provider is selected silently. Existing delivered transcripts can bootstrap new
Codex conversations. Setup and test steps are documented in codex-harness.md.

RSS tools explicitly distinguish all unread records, the current local calendar
day and the most recently completed reminder window. Pagination is a message/tool
size boundary, not a five-article access limit. Web research is an optional tool
with the existing Gemini grounding implementation; it is not the conversation
harness. Tools do not expose another user's records.

## Administrative API and account connections

All `/api/v1/` routes require `Authorization: Bearer <NUDGE_API_TOKEN>`.
An empty `NUDGE_API_TOKEN` disables these routes with `503`. `X-User-ID`
selects the target user only after administrator authentication; it is not
an identity credential. Do not distribute this administrative token to end
users. Test-onboarding and test-digest additionally require `X-Test-Token`.
Slack event and interaction routes authenticate with Slack request signatures,
including URL verification challenges. Bookmark interactions also verify the
clicking Slack user's workspace and ownership.

Set `NUDGE_LINK_SIGNING_KEY` to a dedicated, random 32-byte base64 key, separate
from `FEEDBIN_ENCRYPTION_KEY`, `NUDGE_API_TOKEN` and `NUDGE_DASHBOARD_TOKEN`.
Generate each independently (`openssl rand -base64 32`) and supply them through
your deployment's secret environment. Keep the existing Feedbin encryption key:
changing it makes previously encrypted credentials unreadable. The server fails
startup if the signing or encryption key is invalid. The Go server does not
load `.env` automatically; the process manager must supply these variables.

Account links now include a signed, user-specific, service-specific capability
valid for eight days. Bare `?user_id=...` links from older messages no longer
work. The Feedbin form carries the capability into its POST request; changing
the user ID or using a Raindrop token is rejected. Treat these links as private
bearer credentials and do not log their query strings in a reverse proxy.
Connection pages use no-store/no-referrer headers. OAuth callbacks additionally
require the initiating browser's HttpOnly, SameSite=Lax state cookie. In HTTPS
proxy deployments, forward `X-Forwarded-Proto: https` from a trusted proxy.

The 13:45 onboarding job sends new invitations at most once per ISO week for
accounts still missing a connection, so expired invitations can be replaced.
An administrator can immediately resend signed links using the protected
`POST /api/v1/reminders/test-onboarding` endpoint and `X-User-ID`.
Rotating the signing key invalidates outstanding links. OAuth state remains
process-local, so installation/callback requests need the same server instance.

## Scheduled summaries and reliable delivery

The 23:00 RSS job summarizes the completed local-calendar window
`[previous day 23:00, scheduled day 23:00)`. For example, the October 23 job
covers October 22 23:00 through October 23 22:59:59. Entries at the ending
boundary belong to the next job. Preparation retries keep the original scheduled
instant, including after midnight. On-demand RSS requests use the explicit scope selected by Codex: all unread,
calendar today, or the most recent completed reminder window.

Scheduled onboarding, RSS digests and bookmark messages are first persisted in
`notifications`. A worker claims pending deliveries atomically with
`FOR UPDATE SKIP LOCKED`, a two-minute lease and a unique claim token. It sets
`sent_at` only after Slack confirms delivery. Failures are rescheduled after
30 seconds, then 1, 2, 4, 8, 16, 32 minutes, capped at one hour. Delivery retries
continue without a fixed attempt limit. A crashed worker's expired lease can be
claimed after restart. The worker runs even if new scheduled reminders are
disabled. It polls at most every 15 seconds when idle or after an error.

Each bookmark has its own durable delivery key; one failed bookmark does not
resend the already completed part of the batch. Feedbin digest payloads are
stored once, so delivery retries do not regenerate summaries or incur AI calls.
Preparation errors (Feedbin/AI, Slack DM lookup, enqueue failures) are aggregated
and retried up to five times in the same process. These pre-enqueue attempts are
not durable across restart. A schedule missed while the server is down is not
backfilled automatically.

Delivery is at-least-once: if Slack accepts a message but its response is lost,
or the process dies before recording success, a retry can duplicate that one
message. No cross-system exactly-once guarantee is claimed.

Migration `006_notification_delivery.sql` is applied idempotently on server
startup and needs ALTER/CREATE privileges. Apply migrations 001–003 before
initial startup; 004–006 are embedded startup migrations. Existing notification
rows remain marked sent, so deployment does not replay historical notifications.
The older code did not distinguish failed claims from delivered messages;
historical failures cannot be identified automatically. Stop old application
instances before upgrading, since they do not use the delivery queue.

Validation: `go test -race ./...` and `go vet ./...`. To exercise real PostgreSQL
migration, concurrent claims, backoff, lease recovery and stale-worker fencing,
point `NUDGE_TEST_DATABASE_URL` at a disposable database and run
`go test -race ./internal/repository/postgres -run TestNotificationDeliveryPostgres`.
The integration test creates and removes its own isolated schema. Slack and AI
calls in the other tests are mocked; no real messages or billable calls are sent.
