# Repository tasks: connection and draft phase

Nudgy can connect a Slack user's GitHub account, list permitted repositories,
and persist a task containing their original Slack request. Disabled by default.
This phase does **not** clone repositories, execute code/tests, push branches,
or publish PRs. GitLab is not implemented yet.

## GitHub App setup

Create a GitHub App with OAuth callback
`https://YOUR_NUDGE_HOST/github/oauth/callback`. Enable expiring user access
tokens. Leave **Request user authorization (OAuth) during installation** OFF.
Set **Setup URL** to `https://YOUR_NUDGE_HOST/github/setup` and enable
**Redirect on update**. Make the App public so other users can install it.
The setup redirect starts the separate PKCE OAuth step automatically. Grant repository **Contents: read/write**, **Pull requests: read/write**,
and **Metadata: read**. Install only on selected repositories. No webhook or App
private key is needed in this phase. Write permissions establish eligibility
for future coding tasks; this phase only uses read endpoints.

```text
NUDGY_CODING_ENABLED=true
NUDGY_GITHUB_APP_ID=<numeric App ID>
NUDGY_GITHUB_APP_SLUG=nudgy-github
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
2. Open the private link. The Nudgy page opens GitHub's installation/repository
   selection screen in a new tab. Choose the account and repositories and save.
   GitHub redirects to `/github/setup`, then Nudgy starts PKCE authorization.
   If an existing personal installation needs no selection changes (GitHub
   disables Save), return to the Nudgy page and continue with the existing
   selection. This fallback verifies the authenticated user's own installation.
   Installation and OAuth use separate single-use 10-minute states and HttpOnly
   browser cookies. The returned installation ID is never trusted: verify App ID,
   account ownership (or organization access), permissions, and at least one
   selected writable repository before saving the connection. A declined,
   expired, pending organization approval, or unverified installation never
   replaces an existing connection or produces a success page.
   The confirmation page shows the verified GitHub login and returns to Slack;
   connecting does not list repos to Slack, prepare tasks, or modify code.
3. Ask for repositories. Access requires the App's installation permissions and
   the user's current GitHub write permissions. Suspended installations, archived
   and disabled repositories are excluded. Installation inventory is only a
   candidate list: each candidate also requires `GET /repos/{owner}/{repo}/collaborators/{login}/permission`
   to return the verified user's numeric ID and effective `write` or `admin`
   permission. `permissions.push` on installation inventory alone is insufficient.
   Repositories whose role cannot be verified (403/404) are excluded; network,
   rate-limit (429), and server failures fail the whole listing instead of
   presenting a partial success.
4. Request a change in `owner/repository`. Nudgy saves the original Slack request
   as an immutable **draft**, reports its ID and says execution is unavailable.
   The same user can inspect/cancel it in the same Slack thread.

Tools: `github_connect`, `github_connection_status`, `repositories_list`, `repository_task_prepare`,
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

Before listing repositories or preparing a draft, Nudgy calls GitHub `/user`
with the current user token (including after refresh) and compares the immutable
GitHub ID to the saved connection. Missing identity, mismatched Slack owner,
wrong token subject, or verification failure prevents repository access. A
GitHub login rename is allowed when the numeric ID is unchanged. The repository
tool returns the verified account alongside the list; it must not infer account
identity from repository ownership. `github_connection_status` verifies the
account without listing repository data.

Connection and repository-list logs include internal Slack user ID and GitHub
numeric ID, with repository count for listings; credentials and repository names
are not logged. For an unexpected list, compare this verified identity with the
requesting Slack user's connection before assuming collaboration permissions.
These safeguards do not prove the cause of an earlier incident: a consistently
linked but unintended GitHub account still requires checking the saved mapping
and reconnecting through the correct user's private invitation.

Admin diagnostics (require the existing `NUDGE_API_TOKEN` bearer; never public):
`GET /api/v1/coding/github/connections` lists saved Slack/GitHub bindings without
credentials (bounded to 1,000 connections). `GET /api/v1/coding/github/connection`
with `X-User-ID` verifies that user's current GitHub identity and repository
access. Both responses use `Cache-Control: no-store`; neither sends Slack messages.

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
