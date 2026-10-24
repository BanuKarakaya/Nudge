# Repository tasks: connection and draft phase

Nudgy can connect a Slack user's GitHub account, list permitted repositories,
and persist a task containing their original Slack request. Disabled by default.
This phase does **not** clone repositories, execute code/tests, push branches,
or publish PRs. GitLab is not implemented yet.

## GitHub App setup

Create a GitHub App with OAuth callback
`https://YOUR_NUDGE_HOST/github/oauth/callback`. Enable expiring user access
tokens. Grant repository **Contents: read/write**, **Pull requests: read/write**,
and **Metadata: read**. Install only on selected repositories. No webhook or App
private key is needed in this phase. Write permissions establish eligibility
for future coding tasks; this phase only uses read endpoints.

```text
NUDGY_CODING_ENABLED=true
NUDGY_GITHUB_APP_ID=<numeric App ID>
NUDGY_GITHUB_CLIENT_ID=<App OAuth client ID>
NUDGY_GITHUB_CLIENT_SECRET=<App OAuth client secret>
NUDGY_CODING_ENCRYPTION_KEY=<separate base64-encoded 32-byte random key>
NUDGE_PUBLIC_URL=https://YOUR_NUDGE_HOST
```

Keep the encryption key stable across deployments and back it up securely.
Changing it without migrating credentials requires reconnection. Existing
Slack authentication, DM permissions and link signing must also be configured.
Startup applies the idempotent coding schema.

`NUDGY_GITHUB_ALLOWED_REPOS` is no longer read or required. Remove it from
deployment configuration after deploying this version. Repository selection is
managed in GitHub's App installation (all repositories or selected repositories).
Each list/task request rechecks GitHub; added repositories need no server config
change, and removed permissions stop authorizing new tasks.

## User flow and access

1. After Slack installation, Nudgy automatically sends each user a separate
   GitHub invitation when the GitHub integration is configured. Existing installations
   are discovered at startup too. The signed link is valid for 20 minutes after
   delivery and goes only to that user's DM. Asking Nudgy to connect GitHub is
   still available to replace expired links, but is not needed for discovery.
2. Open it and authorize the App. OAuth uses PKCE, a single-use 10-minute state
   and a browser cookie binding the callback.
3. Ask for repositories. Access requires the App's installation permissions and
   the user's current GitHub write permissions. Suspended installations, archived
   and disabled repositories are excluded.
4. Request a change in `owner/repository`. Nudgy saves the original Slack request
   as an immutable **draft**, reports its ID and says execution is unavailable.
   The same user can inspect/cancel it in the same Slack thread.

Tools: `github_connect`, `repositories_list`, `repository_task_prepare`,
`repository_task_status`, `repository_task_cancel`. Native Codex threads are
renewed when the tool set changes, retaining delivered conversation history.
Local greeting responses still bypass model calls.

Credentials and PKCE verifiers are encrypted in PostgreSQL. Refresh runs under
a row lock; OAuth state is hashed. Identity comes from verified Slack events,
never model arguments. Repeated events return the original task. Authenticated
admin `DELETE /api/v1/coding/github/connection` removes local credentials and
cancels drafts; users can revoke authorization in GitHub settings too.

Slack–GitHub association is automatic and per user within each workspace:
the signed private invitation identifies a persisted Slack user row; that row
already belongs to its Slack installation. OAuth saves the verified GitHub
identity and encrypted user token against that row. Every coding tool validates
the event's workspace against this persisted membership before using credentials.
The same person in another workspace needs a separate connection. Tokens are
never shared with coworkers, and no workspace ID or repository list is entered
manually. Existing connections inherit their workspace from the same user
foreign key, so no credential migration or mass reconnection is needed.

## Next phase

Execution requires a durable queue and isolated per-task workers with resource
limits and temporary scoped credentials. Repository code/tests must not execute
inside the production bot container. Recheck authorization before execution and
PR publication. Existing drafts must not execute automatically after deployment.
Workers will create a branch, run Codex edits/tests and publish a PR with the
actual diff and test report; merging remains separate. Match runners to projects:
PipePulse requires macOS for its native Swift build/tests.

## Verification

Run `go test -race ./...` and `go vet ./...`. Provider/handler tests mock GitHub;
they neither modify external repositories nor spend model credits. Set
`NUDGE_TEST_DATABASE_URL` to a disposable PostgreSQL database and run
`go test -race ./internal/repository/postgres -run TestCodingStorePostgres`
for encryption, locking, event deduplication and ownership checks.

References: [GitHub user tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app),
[refresh](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens),
[installation API](https://docs.github.com/en/rest/apps/installations).
