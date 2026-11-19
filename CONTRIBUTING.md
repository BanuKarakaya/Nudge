# Contributing to Nudgy

Turkish and English contributions are welcome. Use a GitHub issue for a
reproducible bug or a focused feature proposal. For security issues follow
[SECURITY.md](SECURITY.md) instead.

## Local workflow

1. Fork and clone the repository, then create a focused branch.
2. Read [self-hosting](docs/self-hosting.md) and [architecture](docs/architecture.md).
3. Make a small, reviewable change. Preserve workspace/user authorization checks.
4. Format changed Go files with `gofmt`, then run `make check` and `make build`.
5. If persistence changes, run the PostgreSQL tests against a disposable database
   with `NUDGE_TEST_DATABASE_URL` and document the migration/rollback implications.
6. Open a pull request describing behavior, validation and remaining limitations.

Do not include `.env` files, tokens, personal conversation exports, Codex state,
database dumps or production logs. `.env.example` must contain only placeholders
and public local-development defaults. Use synthetic data in tests. New tools
must preserve tenant isolation and check permissions in Go, not just in a prompt.

Regular tests must not call paid AI services, message real Slack users, or mutate
live repositories. The existing live-model test is opt-in; read its documented
limits before deliberately running it with a dedicated account.

For release review, run the full offline integration gate with a disposable
PostgreSQL database and the Codex executable pinned in `Dockerfile`:

```sh
NUDGE_TEST_DATABASE_URL='postgres://nudge:nudge@localhost:5432/nudge_test?sslmode=disable' \
NUDGY_TEST_CODEX_BINARY='/absolute/path/to/pinned/codex' make check-integration
NUDGY_TEST_COMPOSE=1 make test-installation
```

`check-integration` refuses to run without both prerequisites. `make check`
always reruns Go tests with the race detector and also checks the configuration
helper; without those integration variables, database/native tests are skipped.
Three paid model tests and the manual dashboard preview remain opt-in.

CI runs Go vet/race tests, JavaScript tests, PostgreSQL integration tests, a clean
server build, vulnerability checks and secret scanning. Dependency changes should
update `go.sum`, the notices and any bundled license texts. Keep the Docker Codex
pin and runtime protocol tests in sync.

## Licensing and review

By submitting a contribution, you agree that your original contribution can be
distributed under the project's [MIT license](LICENSE). Contribute only material
you are entitled to license and retain third-party copyright/license notices.
There is no CLA or mandatory sign-off process at present. Do not add a dependency
or asset with unknown provenance.

Maintainers review changes before merging. Be respectful, discuss the work
constructively, and avoid sharing anyone's private information. Bug reports should
include the commit/version, environment, minimal reproduction and sanitized error
output; screenshots must hide credentials and private data.
