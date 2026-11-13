# Operations and upgrade guide

## Upgrade behavior

Back up PostgreSQL and all encryption keys together. The server now embeds every
migration, applies them under a database advisory lock, and records a SHA-256
checksum in `nudge_schema_migrations`. Existing installations without a version
table are adopted. Subsequent releases must add migrations rather than edit
recorded files. A checksum mismatch stops startup; do not delete the history to
force an upgrade. The bootstrap script remains compatible with fresh Compose
volumes but is no longer required by the application.

On startup, legacy Slack and Raindrop plaintext tokens are encrypted. Private
native AI state moves to user-scoped directories: old unowned rollout caches are
removed and the next turn recovers the delivered conversation from PostgreSQL.
This deliberately does not preserve undisplayed reasoning/tool history.

External write tools default to **approval**. Workspace managers can select
`approval`, `auto`, or `disabled` at `/dashboard/actions`. Approval is bound to the
requesting person, workspace, channel, thread, exact arguments and a 20-minute
expiry. Pushes also bind the reviewed digest; merges bind the observed source
commit and destination. A revision change requires a new proposal. An interrupted
approved action with an unknown remote outcome is not automatically repeated.
Old push buttons must be replaced with a current approval card.

## Settings

| Variable | Default | Meaning |
| --- | --- | --- |
| `NUDGY_RETENTION_DAYS` | `90` | Terminal jobs/tasks, conversations, analytics, audit history and inactive native conversations; valid range 1–3650 |
| `NUDGY_RUN_TOKEN_LIMIT` | `100000` | Reservation for each run; observed usage stops generation after the limit |
| `NUDGY_USER_DAILY_TOKENS` | `500000` | User reservations/usage per UTC day |
| `NUDGY_WORKSPACE_DAILY_TOKENS` | `2000000` | Workspace reservations/usage per UTC day |
| `NUDGY_WORKSPACE_CONCURRENCY` | `2` | Active reserved runs per workspace; native processes also have a global limit of four per instance |
| `NUDGY_METRICS_TOKEN` | empty | Bearer token for `/metrics`; empty disables it |
| `NUDGY_CUSTOM_AI_BASE_URL` | empty | Operator-selected Responses-compatible endpoint; HTTPS or loopback HTTP |

Token ceilings use reported usage, not provider billing controls. A currently
streaming response can exceed the observed threshold, and a model handoff is a
second stage. Use provider account spending limits for an absolute money ceiling.
Unknown usage retains the full reservation; expired run leases free concurrency.
Google search has its own provider billing and is not included in the AI token
reservation. These limits are deployment defaults, not editable by members.

The custom endpoint must implement Responses and `/models` in the format expected
by the pinned native harness. A generic chat-completions-only local server is not
compatible. The URL is controlled by the operator; workspace users cannot redirect
stored keys to arbitrary hosts. A local server requiring no credential can use a
nonsecret placeholder key. Test that server's protocol before enabling it.

Managers select models in `/dashboard/ai`. The optional tool/model selectors save
an exact tool-name → model-ID map, validated against the provider catalog. The
first matching tool routes the request before execution; the resumed model
finishes that request without further routing, avoiding loops. Existing explicit
advanced-model handoff remains supported.

`/dashboard/preferences` controls each workspace's IANA timezone and daily
reminders. New installations start with reminders disabled. Existing
`NUDGE_REMINDER_USER_ID`/`NUDGE_TIMEZONE` settings are adopted once. Durable daily
claims recover the latest completed 22:00 bookmarks and 23:00 Feedbin window;
older missed days are not flooded into Slack.

## Key rotation

Each encryption variable accepts its existing 32-byte base64 value or a JSON
keyring:

```json
{"active":"2026-11","keys":{"2026-11":"NEW_BASE64_KEY","previous":"OLD_BASE64_KEY"}}
```

When the previous deployment used a single base64 key and already wrote `v2`
ciphertext, use its existing key ID (the first 16 hex characters of SHA-256 of the
base64 text), not the example word `previous`. The ID is also visible in the
`v2:ID:` prefix. Legacy `v1` ciphertext is tried against retained keys. Newly
written ciphertext includes the active key ID and binds its table/user/team/task
context as authenticated data. Copying it to a different identity fails.

Slack/Raindrop values are rewritten at startup. Other encrypted records rotate
when rewritten; retain old keys until those records and relevant backups have
been migrated or expired. Changing `active` alone does not re-encrypt every row.
Never print keyring values in logs. Link-signing keys are separate and do not use
this JSON format; rotating them invalidates old links.

## Data and observability

`/dashboard/data` exports profile/bookmarks/delivered conversations/activity and
can delete personal application data, provider connections, sessions and native
rollouts. Workspace membership, shared manager configuration, quota accounting
and the deletion audit remain. Provider-side messages/issues/branches, backups
and external AI provider retention are outside this deletion. An in-flight remote
request may already have committed; deletion cannot recall it.

`/dashboard/audit` provides the manager's latest 200 configuration/action audit
entries. Logs are JSON with request, job, thread and tool identifiers. `/metrics`
returns queue and connection-pool gauges without message content. No OpenTelemetry
collector is required; distributed traces are not implemented.

Queue attempts stop at eight, including abandoned leases. `failed` items no longer
block newer conversation jobs. Do not blindly reset failed external writes:
inspect the provider outcome first. Slack delivery remains at-least-once; a crash
after Slack accepts a message but before its receipt is saved can duplicate it.

Register `app_uninstalled` and `tokens_revoked` Slack events. Signed uninstall/bot
revocation events clear the installation token, disable integration scope,
invalidate panel sessions and stop pending work. User-only token revocation does
not uninstall the workspace. Events predating the latest install are ignored.

OAuth state, queues, approvals and schedule claims are shared in PostgreSQL.
Multiple replicas must use the same signing/encryption configuration. Native
rollouts may be per-replica: a missing rollout restarts from delivered history.
Each replica needs its own writable state directory; do not concurrently mutate a
shared native rollout. Edge rate limits are still needed behind a reverse proxy:
the application uses `RemoteAddr`, not untrusted forwarded client addresses.

## Quality and releases

`make check` runs formatting, vet, race tests and dashboard tests. Set
`NUDGE_TEST_DATABASE_URL` to a disposable database and `NUDGY_TEST_CODEX_BINARY` to
the pinned CLI to include real PostgreSQL and offline native protocol checks.
CI supplies both. `make lint` runs pinned Staticcheck. CI additionally runs
vulnerability/secret scans and scans the built container with
[Trivy](https://trivy.dev/docs/latest/references/configuration/cli/trivy_image/).

The runtime uses Node 22 on Alpine and the pinned musl Codex binary. npm,
Corepack and Yarn are removed after CLI installation; update dependencies by
rebuilding the image. Node remains available for the CLI launcher. The Go build
cache uses a temporary memory mount and is not retained in a build layer.

The nightly eval workflow repeats deterministic injection/approval/merge/routing
and native-protocol tests three times, with zero paid inference. Its per-case
summary identifies the harness version; this is not a live model quality score.
Live model tests remain explicit opt-ins requiring provider credentials and
provider-side spending limits. No paid tests are run by the default workflow.

`scripts/release.sh vMAJOR.MINOR.PATCH` runs quality gates and prepares Linux
binaries with checksums. Review `CHANGELOG.md`, test an upgrade on a database copy,
then create a signed tag and publish through the maintainer's release process.
The script does not tag, push or publish automatically.
