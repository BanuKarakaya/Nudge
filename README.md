# Nudgy

Nudgy is a self-hostable Slack assistant for repository work, reading lists and
team tasks. The Go server, customer dashboard, database schemas, integration
code and landing-page source are available in this repository under the
[MIT license](LICENSE), including commercial use, modification and redistribution.

**Türkçe:** Nudgy; Slack üzerinden kod değişiklikleri hazırlayan, okuma
listelerini takip eden ve ekip araçlarına bağlanan, kendi sunucunda
çalıştırabileceğin açık kaynak bir asistandır.

## What it does

- Handles Slack mentions with per-user conversation context and a durable job queue.
- Prepares GitHub/GitLab code changes on new branches; supports separately requested PRs/MRs and explicit merges.
- Connects Feedbin, Raindrop, Jira Cloud and Trello accounts.
- Provides a dashboard for workspace setup, personal accounts, activity and usage.
- Keeps each workspace's AI credentials and integration permissions separate.

There is no license key, paid edition gate or private application package needed
to build this source. **Self-hosted does not mean offline:** Slack is required
for the supported user workflow. AI turns currently use a workspace-owned
OpenAI or OpenRouter API account through the pinned Codex app-server.
Google-powered search and the other integrations are optional. These hosted
services have their own terms, availability and charges. Local model servers,+self-hosted GitLab and non-Slack sign-in are not currently supported.

## Quick start (Docker)

Requirements: Git, Docker with Compose v2, and Python 3 for the configuration
helper. A browser can view the local login page without provider credentials;
actual Slack use requires your own Slack app and a reachable HTTPS origin.

```sh
git clone https://github.com/BanuKarakaya/Nudge.git
cd Nudge
python3 scripts/setup-env.py
# Edit .env: enter your Slack credentials and public URL (see the guide below).
docker compose --profile app up -d --build --wait
curl --fail http://localhost:8080/health/db
```

Open <http://localhost:8080/dashboard>. To connect Slack, follow
[the self-hosting guide](docs/self-hosting.md) for OAuth, event subscriptions and
interactivity. The installer becomes the workspace manager and configures their
own AI key and models in **AI settings**. Optional integrations start disabled.

The helper creates independent random keys and refuses to overwrite an existing
`.env`. PostgreSQL initializes the three base migrations only on a **fresh** data
volume; the server applies the remaining embedded, idempotent schemas on startup.
Compose retains database and Codex state in named volumes. See the guide before
upgrading an existing installation. Do not delete volumes to resolve a migration
error: they contain application data.

## Develop and test

Native development requires Go (the version in `go.mod`), Git and Node.js 22+
for the dashboard tests. The supported server targets are Linux and macOS;
use Docker or WSL2 on Windows. The Codex version is pinned in `Dockerfile`.

```sh
make test                 # Go tests + browser-code tests; no real provider calls
make check                # vet + race-enabled Go tests + browser-code tests
make build                # bin/nudge-server
```

Database integration tests require a **disposable** PostgreSQL database:

```sh
NUDGE_TEST_DATABASE_URL=postgres://nudge:nudge@localhost:5432/nudge_test?sslmode=disable go test -race ./internal/repository/postgres
```

Tests create and remove their own schemas. Do not point them at production.
Paid live-model tests are opt-in and are not enabled by these commands or CI.
See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow.

## Project map

| Path | Purpose |
| --- | --- |
| `cmd/server` | Server configuration, routes and background workers |
| `internal/handler` | Slack/OAuth callbacks and customer dashboard |
| `internal/service`, `internal/codex` | Assistant tools and isolated AI runtime |
| `internal/coding`, `internal/tracker` | Repository and task integrations |
| `internal/repository/postgres`, `migrations` | Persistence and database schemas |
| `internal/dashboard` | Embedded styles/assets and legacy dashboard tests |
| `railway/nudgy-tanitim` | Committed landing-page source and container configuration |
| `scripts` | Local configuration and empty-database initialization |

## Documentation

- [Self-hosting and configuration](docs/self-hosting.md)
- [Architecture, external services and stored data](docs/architecture.md)
- [Dashboard](docs/dashboard.md) and [workspace permissions / AI configuration](docs/workspace-panel.md)
- [GitHub](docs/repository-tasks.md), [GitLab](docs/gitlab.md), [Jira/Trello](docs/jira-trello.md)
- [PR/MR operations](docs/change-requests.md), [Slack jobs](docs/slack-jobs.md), [conversations](docs/conversations.md)
- [Codex runtime](docs/codex-harness.md)
- [Open-source readiness review](docs/open-source-review.md)

## Contributing, security and license

Contributions and reports in Turkish or English are welcome. Start with
[CONTRIBUTING.md](CONTRIBUTING.md). Report vulnerabilities through the private
process in [SECURITY.md](SECURITY.md), not a public issue.

[MIT](LICENSE) covers the original project code, documentation and maintainer-created
Nudgy mascot assets. Third-party components retain their licenses; see
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) and `LICENSES/` before distributing
binaries or images. The MIT license does not grant rights to third-party service
names or trademarks.
