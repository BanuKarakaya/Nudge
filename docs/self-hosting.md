# Self-hosting Nudgy

This guide describes the current workspace-owned AI configuration. Use your own
provider applications and accounts; no credential from the public demo is required.

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

`scripts/init-db.sh` applies **only the Up sections** of migrations 001–003 in a
single transaction. PostgreSQL's entrypoint runs it on new volumes. The server
then applies the other schemas through the repository constructors. Never run
the complete Goose SQL files directly through `psql`: they also contain Down
sections that remove tables.

For an existing database, back up first. Existing initialized Compose volumes
do not rerun init scripts. An empty existing database can be initialized once:

```sh
docker compose exec -T postgres sh /docker-entrypoint-initdb.d/10-nudgy.sh
```

If tables already exist, do not rerun bootstrap; compare schema/migration history
and apply only missing changes. Bootstrap fails transactionally on an existing
base schema instead of resetting it. Do not mix an external migration manager
with startup migrations without reconciling ownership/history first.

## 3. Register your Slack app

Create an app for your Slack workspace and configure these URLs using your own
HTTPS origin:

| Slack setting | URL / value |
| --- | --- |
| OAuth redirect | `/slack/oauth/callback` |
| Event subscriptions Request URL | `/slack/events` |
| Bot event subscription | `app_mention` |
| Interactivity Request URL | `/slack/interactions` |
| Bot scopes | `app_mentions:read`, `chat:write`, `reactions:write`, `users:read`, `im:write`, `channels:history`, `groups:history`, `im:history`, `mpim:history` |

These scopes match `internal/slack/oauth.go`. History scopes allow context from
threads the bot can access. The server currently processes app mentions; enabling
additional message events does not add direct-message conversation support.
The dashboard's separate Slack OpenID Connect sign-in requests `openid profile`
and uses the same callback. Configure Sign in with Slack for your app as required
by Slack; see [Slack's sign-in documentation](https://docs.slack.dev/authentication/sign-in-with-slack/).

Set the client ID, client secret, signing secret and redirect URL in `.env`, then
recreate the app service. Visit `/slack/install` to authorize installation. Invite
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

Run one application replica. OAuth state and native Codex state are not designed
for arbitrary load balancing; a durable Slack job queue alone does not make the
whole application horizontally scalable. `/health` checks the HTTP process;
`/health/db` checks database reachability, not Slack/AI/provider readiness.

Back up PostgreSQL, Codex state and the independent encryption keys. Limit access
to all three. Slack bot tokens and Raindrop OAuth tokens are currently stored
without application-level encryption; use encrypted storage/backups and restricted
database access. See [the architecture/data inventory](architecture.md).

Test an upgrade on a restored copy first. Stop old application instances before
starting the replacement with the same persistent state. `docker compose down`
stops local services; adding `-v` deletes named volumes and is not an upgrade step.

The landing page in `railway/nudgy-tanitim/public/` can be hosted separately.
Its existing links and deployment metadata refer to the maintainer's demo;
replace them with your own URLs if publishing a fork. Railway is optional and is
not required for the Go application.
