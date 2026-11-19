# Self-hosting Nudgy

Nudgy runs on your own infrastructure: your server, PostgreSQL database, Slack
application and provider accounts. The project does not offer hosted workspace
signup. Do not use a maintainer deployment or its Slack OAuth credentials.

One server accepts one Slack workspace, selected by its operator with
`SLACK_TEAM_ID`. An empty value disables Slack installation and workspace access.
Changing the workspace selector does not migrate or delete existing data; use
a separate database and independent encryption keys for a new installation.

## 1. Configuration

Run `python3 scripts/setup-env.py` in the repository root. It copies the example,
generates five independent 32-byte base64 keys, writes `.env` with permissions
0600, and refuses to change an existing file. Alternatively copy `.env.example`
and generate each key separately with `openssl rand -base64 32`.

| Setting | Requirement |
| --- | --- |
| `DATABASE_URL` | PostgreSQL connection; Compose overrides it with its internal database address |
| `PORT` | Defaults to 8080; Compose fixes the internal port at 8080 |
| `NUDGE_PUBLIC_URL` | Canonical HTTPS origin, no path; HTTP accepted only for localhost development |
| `NUDGE_LINK_SIGNING_KEY` | Required independent 32-byte base64 key |
| `FEEDBIN_ENCRYPTION_KEY` | Required even when Feedbin is unused; also the legacy AI encryption fallback |
| `SLACK_TEAM_ID` | Your workspace ID (`T...`), required for installation and access; find it in your Slack browser URL |
| `SLACK_CLIENT_ID`, `SLACK_CLIENT_SECRET`, `SLACK_SIGNING_SECRET` | Your Slack application's credentials |
| `SLACK_REDIRECT_URL` | Exact registered callback: `https://YOUR_HOST/slack/oauth/callback` |
| `NUDGY_AI_ENCRYPTION_KEY` | Recommended independent key; choose before storing AI credentials |
| `NUDGY_CODEX_STATE_DIR` | Persistent absolute writable path; container default `/var/lib/nudgy/codex` |
| `CODEX_BINARY` | Defaults to `codex`, installed by the Dockerfile |
| `NUDGE_TIMEZONE` | IANA timezone, default `Europe/Istanbul` |
| `NUDGE_REMINDER_USER_ID` | 0 disables reminders; a positive internal user ID enables that user's daily schedule |

Keep encryption/signing keys stable across restarts and back them up separately
from the database. Changing them without a migration invalidates links or makes
stored credentials unreadable. The server does **not** load `.env` by itself.
Compose's `env_file` injects it for the app service.

AI API keys and model IDs are configured by each manager in `/dashboard/ai`.
Server-level `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `GEMINI_API_KEY` and model
environment variables are not a production fallback. A successful catalog check
does not prove that every listed model supports the runtime's Responses/tool
protocol; verify the selected model with a short, deliberate test request.

## 2. Start the database and server

If host ports 8080 or 5432 are already occupied, set `NUDGY_HTTP_PORT` and
`NUDGY_POSTGRES_PORT` in `.env` to unused ports. Internal container ports remain
8080 and 5432. Use the chosen HTTP port in local URLs/health checks and set
`NUDGE_PUBLIC_URL` and Slack callbacks to the address users actually visit.

```sh
docker compose --profile app up -d --build --wait
curl --fail http://localhost:8080/health
curl --fail http://localhost:8080/health/db
docker compose logs --tail=100 app
```

The default Compose file is a local, single-host starting point. Its database
password is a public development value; ports bind only to `127.0.0.1`. For an
internet-facing deployment put HTTPS in front of the server, use private database
networking and replace development database credentials. Do not expose Postgres
to the internet. The app runs as uid/gid 1000; the Codex volume must be writable
by that user. There is no Docker socket mount and no public Codex process port.

The server embeds the full schema and applies versioned migrations under an
advisory lock, including on an empty database. It records checksums in
`nudge_schema_migrations` and adopts legacy initialized installations.
The Compose bootstrap script remains compatible but is not required by the server.
Do not run complete Goose SQL files with `psql`: they include Down sections.
Back up first and read [operations](operations.md) before upgrading. Never modify
already recorded migrations or delete their version records to bypass checksums.

### Using an existing PostgreSQL server

The default Compose file supplies its own PostgreSQL service and overrides
`DATABASE_URL`. To use your company's existing PostgreSQL server, set its
connection URL in `.env`, then use the standalone alternative:

```sh
docker compose -f docker-compose.external.yml up -d --build --wait
```

This starts only Nudgy and preserves `DATABASE_URL` from `.env`. The address must
be reachable from inside the container; `localhost` refers to the container
itself. Use the TLS settings required by your database provider and a dedicated
database/user allowed to create and alter Nudgy's tables. The server applies all
migrations automatically. Do not start both Compose variants on port 8080.

On Railway, deploy the repository's Dockerfile and set the same environment
variables directly in the service. Use your own PostgreSQL URL, configure a
public HTTPS domain and `/health/db` healthcheck, and mount writable persistent
storage at `/var/lib/nudgy/codex` (uid/gid 1000). Railway does not require Compose.

## 3. Register your Slack app

Create your own Slack app for your workspace using
[`internal/slack/manifest.json`](../internal/slack/manifest.json). Replace every
`https://YOUR_NUDGY_HOST` with your own HTTPS origin before importing the manifest.
Keep public distribution disabled; users of other workspaces deploy their own
instance. In **Basic Information → Display Information**, upload
[`nudgy-icon.png`](../assets/slack/nudgy-icon.png) once for your app.
An independently created Slack app does not inherit the maintainer’s icon.
The [Slack asset pack](../assets/slack/README.md) also includes the optional
thinking emoji and 11 animated GIFs, with download and usage instructions.

Configure these URLs using your own HTTPS origin:

| Slack setting | URL / value |
| --- | --- |
| OAuth redirect | `/slack/oauth/callback` |
| Event subscriptions Request URL | `/slack/events` |
| Event subscriptions | `app_mention`, `message.im`, `app_uninstalled`, `tokens_revoked` |
| Interactivity Request URL | `/slack/interactions` |
| Bot scopes | `app_mentions:read`, `chat:write`, `reactions:write`, `users:read`, `im:write`, `channels:history`, `groups:history`, `im:history`, `mpim:history` |

These scopes match `internal/slack/oauth.go`. History scopes allow context from
threads the bot can access. The manifest also enables writable direct messages through `message.im`.
The dashboard's separate Slack OpenID Connect sign-in requests `openid profile`
and uses the same callback. Configure Sign in with Slack for your app as required
by Slack; see [Slack's sign-in documentation](https://docs.slack.dev/authentication/sign-in-with-slack/).

Set your workspace ID (`SLACK_TEAM_ID`), client ID, client secret, signing secret
and redirect URL in `.env`, then
recreate the app service. Visit **your own HTTPS origin** followed by `/slack/install` to authorize installation.
The server checks the workspace returned by Slack before saving the bot token;
a different workspace is rejected even if the OAuth URL’s `team` hint is changed. Invite
the bot to a channel and mention it. Open `/dashboard`; the installer configures
the workspace's AI provider and models. Members use **Sign in with Slack**.

For local Slack testing, use an HTTPS reverse proxy/tunnel and set both public
URL and callbacks to that origin. Merely running localhost does not let Slack
deliver events to your machine.

## 4. Optional integrations

All repository/tracker integrations start disabled. Each requires an app you own,
the appropriate environment settings, a manager's shared resource selection and
each user's personal authorization.

| Integration | Environment configuration | Setup |
| --- | --- | --- |
| GitHub | `NUDGY_CODING_ENABLED=true`, `NUDGY_GITHUB_APP_ID`, `NUDGY_GITHUB_APP_SLUG`, `NUDGY_GITHUB_CLIENT_ID`, `NUDGY_GITHUB_CLIENT_SECRET`, `NUDGY_CODING_ENCRYPTION_KEY` | [GitHub App guide](repository-tasks.md) |
| GitLab.com | `NUDGY_GITLAB_ENABLED=true`, `NUDGY_GITLAB_CLIENT_ID`, `NUDGY_GITLAB_CLIENT_SECRET`, `NUDGY_CODING_ENCRYPTION_KEY` | [GitLab guide](gitlab.md) |
| Jira Cloud | `NUDGY_JIRA_ENABLED=true`, `NUDGY_JIRA_CLIENT_ID`, `NUDGY_JIRA_CLIENT_SECRET`, `NUDGY_TRACKER_ENCRYPTION_KEY` | [Tracker guide](jira-trello.md) |
| Trello | `NUDGY_TRELLO_ENABLED=true`, `NUDGY_TRELLO_CLIENT_ID`, `NUDGY_TRELLO_CLIENT_SECRET`, `NUDGY_TRACKER_ENCRYPTION_KEY` | [Tracker guide](jira-trello.md) |
| Raindrop | `RAINDROP_CLIENT_ID`, `RAINDROP_CLIENT_SECRET`, `RAINDROP_REDIRECT_URL` | Callback `/raindrop/oauth/callback`; personal account connection |
| Feedbin | Base encryption key above; users supply their own Feedbin credentials | Personal account connection |
| Web search | Workspace Google AI key/model in `/dashboard/ai` | No server-wide Google key |

Legacy `/api/v1/*` and `/api/dashboard*` routes are unavailable to customers.
An old admin/API token does not replace dashboard sign-in. `NUDGE_TEST_TOKEN`
remains a legacy handler option and does not enable those routes.

## Native development

Start Postgres with `docker compose up -d --wait postgres`, or use an empty local
database and `DATABASE_URL=... sh scripts/init-db.sh`. Install the Codex version
from `Dockerfile` if testing real AI turns. Unit tests use fakes and do not need it.
Export your own trusted environment file before starting the server:

```sh
set -a
. ./.env
set +a
export NUDGY_CODEX_STATE_DIR="$PWD/.local/codex"
mkdir -p "$NUDGY_CODEX_STATE_DIR"
make run
```

Shell-sourced `.env` values must be correctly quoted if they contain shell
metacharacters. Never source a configuration file from an untrusted source.

## Operations and data

Start with one application replica. OAuth state, sessions and job queues are
stored in PostgreSQL. Native Codex state needs a writable directory per replica;
see [operations](operations.md) before scaling. `/health` checks the HTTP process;
`/health/db` checks database reachability, not Slack/AI/provider readiness.

Back up PostgreSQL, Codex state and the independent encryption keys. Limit access
to all three. Slack bot tokens and Raindrop OAuth tokens are encrypted by the
production server using the base encryption key; legacy plaintext records are
migrated on startup. Use encrypted storage/backups and restricted database access
as well. See [the architecture/data inventory](architecture.md).

Test an upgrade on a restored copy first. Stop old application instances before
starting the replacement with the same persistent state. `docker compose down`
stops local services; adding `-v` deletes named volumes and is not an upgrade step.

The landing page in `railway/nudgy-tanitim/public/` can be hosted separately.
Its existing links and deployment metadata refer to the maintainer's demo;
replace them with your own URLs if publishing a fork. Railway is optional and is
not required for the Go application.

## Internal application connections

Managers can add remote MCP endpoints from the dashboard. Public HTTPS endpoints
work by default; private network access requires an operator origin allowlist and
actual network connectivity from the deployment. See [MCP connections](mcp-connections.md)
for service-account permissions, supported authentication, setup and limitations.

## Upgrading from an unrestricted installation

Set `SLACK_TEAM_ID` to the operator-owned workspace ID before deploying this
version. The restriction applies to OAuth, Slack events/interactions, queued Slack
requests, dashboard sessions and bot-token access. Other stored workspace records
are retained but cannot use this instance through those paths. No data is deleted
automatically. A blank selector fails closed; it never allows the first visitor
to claim the server.
