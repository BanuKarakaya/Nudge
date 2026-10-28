# Repository editing on Railway

Nudgy can connect a Slack user's GitHub account, list permitted repositories,
clone repositories into separate temporary Git workspaces on the existing Railway
service, and edit source files through bounded tools. Disabled unless
`NUDGY_CODING_ENABLED=true`. Prepared source is encrypted in PostgreSQL so it
survives Railway restarts. Automatic tests, repository scripts, PRs and merges
are not run. A later explicit user approval publishes the reviewed code to a
new `codex/nudgy-<task-id>` branch. GitLab is not implemented yet.

## GitHub App setup

Create a GitHub App with OAuth callback
`https://YOUR_NUDGE_HOST/github/oauth/callback`. Enable expiring user access
tokens. Leave **Request user authorization (OAuth) during installation** OFF.
Set **Setup URL** to `https://YOUR_NUDGE_HOST/github/setup` and enable
**Redirect on update**. Make the App public so other users can install it.
The setup redirect starts the separate PKCE OAuth step automatically. Grant repository **Contents: read/write**, **Pull requests: read/write**,
and **Metadata: read**. Install only on selected repositories. No webhook or App
private key is needed. The linked user access token authorizes cloning and,
after approval, pushing the new branch. The runtime image includes Git.

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
   selection screen in the same tab. Choose the account and repositories and save.
   GitHub redirects to `/github/setup`, then Nudgy starts PKCE authorization.
   The page has one action: select repositories on GitHub. It does not offer a
   separate existing-selection button. GitHub may show installation settings
   for accounts that already installed the App; saving a repository selection
   update returns through the same setup callback. The old POST continuation
   remains supported for already-open pages, but is not offered in the new UI.
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
4. Request a change in `owner/repository`. Nudgy pins the verified GitHub account,
   repo, default-branch commit and original signed Slack request. It clones the
   repo into a separate temporary bare Git directory, reads source, and saves
   actual file edits in the encrypted task workspace. Native shell/host file
   tools remain disabled. No project hooks, filters, submodules or tests run.
5. Finishing freezes the changed bytes, base commit, branch and summary under a
   SHA-256 review digest. A private DM contains a 24-hour review link, before/after
   source views, and a ZIP of the complete edited working copy (without `.git`).
   The user tests that copy in their own environment. `tests=not_run` is always
   shown; the model cannot manufacture successful test results.
6. The user sends `pushla <task-id> <full-review-digest>` in the **same Slack
   conversation**, mentioning Nudgy when needed. The host matches the entire
   current signed message, originating owner/workspace/channel/thread and a later
   event. Model arguments, previous conversation, repository text, vague OKs and
   the preparation event cannot authorize push. If files change, the old review
   link and approval become invalid.
7. Current account and repository write permissions are verified again. Git
   reconstructs the frozen changes, creates a deterministic commit and pushes
   only its new branch with an explicit empty ref lease. Existing unrelated refs
   cannot be replaced; the default branch is never updated. If the default branch
   moved, prepare a new task against current code and test again. Successful-push
   retries recover the same commit even if the response/database write was lost.
   Row locks serialize concurrent approvals across service instances.

The turn retains the existing 120-second agent and 45-second tool budgets. Large
changes can require more than one Slack turn. `repository_tasks_list` recovers
saved tasks after a timeout/restart; read status and continue the same task.
A failed clone remains cancellable. Old legacy drafts never start automatically.

Tools: `github_connect`, `github_connection_status`, `repositories_list`, `repository_task_prepare`,
`repository_task_status`, `repository_task_cancel`, `repository_tasks_list`,
`repository_file_read`, `repository_file_write`, `repository_edit_finish`,
`repository_push`. Native Codex threads are
renewed when the tool set changes, retaining delivered conversation history.
Local greeting responses still bypass model calls.

Credentials and PKCE verifiers are encrypted in PostgreSQL. Refresh runs under
a row lock; OAuth state is hashed. Identity comes from verified Slack events,
never model arguments. Repeated events return the original task. Authenticated
admin `DELETE /api/v1/coding/github/connection` removes local credentials and
cancels pending edits and drafts; users can revoke authorization in GitHub settings too.

Slack–GitHub association is automatic and per user within each workspace:
the signed private invitation identifies a persisted Slack user row; that row
already belongs to its Slack installation. OAuth saves the verified GitHub
identity and encrypted user token against that row. Every coding tool validates
the event's workspace against this persisted membership before using credentials.
The same person in another workspace needs a separate connection. Tokens are
never shared with coworkers, and no workspace ID or repository list is entered
manually. Existing connections inherit their workspace from the same user
foreign key, so no credential migration or mass reconnection is needed.

Before accessing repository tasks, Nudgy calls GitHub `/user`
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

## Boundaries

The app runs only fixed Git commands on the existing Railway service. Credentials
are supplied only to Git network subprocesses through a minimal environment;
no token is stored in Git URLs/config, exposed to the model, or included in an
archive. Hooks, inherited Git configuration, redirects and non-HTTPS transports
are disabled. Repository content is read as Git blobs, never executed.

Working copies are limited to 32 MiB and 5,000 regular files; symlinks/submodules
are rejected explicitly. Clone disk usage is monitored against 128 MiB and each
Git operation has a 40-second budget. Each user may have 10 open tasks. Edits are
limited to 200 files / 8 MiB total, with 1 MiB per text write. GitHub Actions
workflow edits are rejected because that extra permission was not requested.
Pending source, original instructions and frozen reviews are encrypted at rest.
Explicit task cancellation clears its source; disconnect invalidates pending
reviews. Completed reviews currently remain stored.

Automatic builds/tests would need an execution sandbox separate from the bot.
They are outside this manual-testing workflow. Neither connecting GitHub nor
requesting a repository list starts a coding task or writes to GitHub.

## Verification

Run `go test -race ./...` and `go vet ./...`. Provider/handler tests mock GitHub;
they neither modify external repositories nor spend model credits. Set
`NUDGE_TEST_DATABASE_URL` to a disposable PostgreSQL database and run
`go test -race ./internal/repository/postgres -run TestCodingStorePostgres`
for encryption, locking, event deduplication and ownership checks.

References: [GitHub user tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app),
[refresh](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens),
[installation API](https://docs.github.com/en/rest/apps/installations).
