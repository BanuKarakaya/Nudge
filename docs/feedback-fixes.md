# Workspace onboarding and request inspection

This change addresses the November 16 installation and dashboard feedback. It
requires the new `014_model_inspector.sql` migration, which runs automatically
at startup. Deploy the new server binary/container; existing processes do not
pick up source changes.

Migration 014 adds tables and columns; it does not drop existing messages, usage
or account connections. Application rollback keeps this additive schema in place.
Do not drop the new tables or remove the migration checksum to roll back the
application: that would discard captured model calls and HTTP error records.

Release validation on November 18 covered the previous 013 schema with retained
messages and usage, two consecutive migration runs, owner-only content access,
PostgreSQL-backed race tests, the pinned native runtime, and 15 dashboard
JavaScript tests. Six real-model scenarios (three each on DeepSeek and GPT)
also passed with the provider observer enabled and synthetic tool effects.
An interrupted transport after an already forwarded `response.completed` event
is treated as completion; a stream without that event remains an error.

## Integration setup

Installing Nudgy into Slack does not register third-party OAuth applications.
Each self-hosted deployment needs its own GitHub App / GitLab / Jira / Trello /
Raindrop application credentials. The same configured server can serve multiple
Slack workspaces; members do not need to create separate provider applications.

Accounts now distinguish missing **server setup** from a workspace manager not
having enabled an integration. Managers can expand the card to see exact env
variable names and the canonical callback URL. Set those values on the server,
restart, then connect the manager's account and choose shared resources from
Integrations. Credentials and server flags cannot be manufactured from code.

Provider-specific permissions and registration steps:
[GitHub](repository-tasks.md), [GitLab](gitlab.md), [Jira/Trello](jira-trello.md).

## Slack appearance and direct messages

All profile images and animations are also available in the standalone
[Slack asset pack](../assets/slack/README.md), with previews and manual upload instructions.

The manager's Setup page downloads a manifest with this installation's URLs,
a 512px app icon and a 128px optional custom emoji. The source manifest is
[`internal/slack/manifest.json`](../internal/slack/manifest.json). Set the real
HTTPS host before importing it into Slack. Existing apps must preserve any
additional custom configuration when adopting the template.

1. In Slack app settings, upload
   `internal/dashboard/static/nudgy-icon.png` as the App Icon. This is an app
   setting and is inherited by installations of that same app.
2. Optionally upload `internal/dashboard/static/nudgy-thinking-emoji.png` in each
   workspace's Custom Emoji settings as `nudgy-thinking-emoji`. Custom emojis
   are workspace-specific; ordinary bot OAuth cannot install them universally.
3. If the custom emoji is absent, Nudgy uses Slack's `thinking_face` and removes
   the exact reaction it added when processing ends. Recovery cleans both names.
4. The manifest subscribes to `message.im`; users can talk to Nudgy directly.
   Bot messages, message edits and unmentioned channel messages are ignored.
   The Messages tab is writable. Reinstall if Slack requests updated permissions.

For operator automation, `scripts/slack-branding.py --app-id A... --icon-url
https://your-host/dashboard/nudgy-icon.png` calls Slack's official
[apps.icon.set](https://docs.slack.dev/reference/methods/apps.icon.set/) using a
one-time `SLACK_APP_CONFIG_TOKEN` with `app_configurations:write`. Do not put this
powerful configuration token in the runtime bot environment. The ordinary bot
token cannot set app icons. The script is not run automatically.

## Request inspector

- Summary cards and a bounded daily chart replace long daily lists.
- Select a request to see your message, the prepared Nudgy answer and delivery
  status, individual provider calls, tokens, cached input, reasoning tokens,
  provider-reported USD cost and bounded stage history.
- The call inspector exposes redacted input/output JSON plus a message list with
  system/user/assistant/tool roles. It does not guess per-message token counts.
- Cost is recorded only when the provider returns `usage.cost`. Missing cost is
  **unknown**, not free. Coverage shows how many calls reported a cost.
- An authenticated loopback observer captures the actual Responses transport.
  Streaming bytes are forwarded unchanged. Per-call usage replaces native
  cumulative usage in observed runs, avoiding double counting across tools or
  model handoffs. Failed attempts remain in the trace.
- Model content is encrypted with the workspace AI encryption key (the server's
  `NUDGY_AI_ENCRYPTION_KEY`, or its existing fallback). Only the request owner can
  read content. Workspace managers can read team operational metadata but cannot
  inspect another member's conversation or tool output.
- Normal list polling contains no message/model contents. Only selecting a job
  loads content. Up to 128 calls and 128 stage events are shown per request.
  Each request/response capture is capped at 120KB, with a visible truncation
  notice. Counters include every recorded usage event beyond the content cap.
- No provider/auth headers are stored. Known credentials and signed URL queries
  are redacted. Redaction is defense in depth: arbitrary sensitive prose is still
  private content, so ownership, encryption and retention remain necessary.
- Model traces cascade with job deletion; HTTP errors follow retention and
  personal-data deletion. Old requests cannot acquire past provider bodies or
  costs retroactively.

## Errors and reverse proxies

`is_bot` now decodes correctly. Slack permanent failures such as `cant_dm_bot`,
revoked credentials and missing scopes stop retrying. PostgreSQL constraint
violations and missing installations are also terminal. Transient connection,
429 and 5xx errors keep their bounded retries.

HTTP logs include request ID, matched route, method, status and duration, never
query strings, credentials or bodies. Signed-in dashboard HTTP failures appear
in the dashboard's error table. Requests that never reached the server, errors
before authentication and database outages still require deployment logs.

Set `NUDGY_TRUSTED_PROXIES` to a comma-separated list of proxy CIDRs you control.
Only a trusted direct peer's `X-Forwarded-For` is used, walking from right to left
to the first untrusted hop. The proxy must append or overwrite that header.
An empty setting retains direct-peer limits. Do not configure `0.0.0.0/0`.

## Remaining feature proposals from the feedback

Socket Mode, App Home, slash commands, file ingestion, sandboxed code execution,
provider adapters beyond Responses, multilingual dashboard copy and published
container releases are separate feature proposals. This change adds DM support
and install assets but does not claim those other capabilities. Repository test
execution must still be reported honestly as unavailable by Nudgy's tools.

## Local navigable preview

Do not serve the generated `overview.html` with a plain file server when testing
navigation: its links use real `/dashboard/...` application routes.

Run the read-only, in-memory preview with the actual dashboard handlers:

```sh
NUDGY_PREVIEW_ADDR=127.0.0.1:18891 go test ./internal/handler -run '^TestDashboardPreviewServer$' -timeout 0 -count=1 -v
```

Open `http://127.0.0.1:18891/dashboard`. The old `/overview.html` URL redirects
there. Sidebar pages, request inspection, polling and sample exports work.
A visible banner identifies sample data; mutation controls and all non-GET/HEAD
requests are disabled. The preview never reads `.env`, connects to a database,
or uses real Slack/provider credentials. It binds only to loopback and is
compiled only into the test binary, never the production server. Keep the
command running while using the preview; Ctrl-C stops it.
