# GitHub PR and GitLab MR operations

Nudgy can create a pull/merge request from a published coding task and query or
explicitly merge an existing request in a currently authorized repository.
GitHub uses the existing App user token (`Contents: write`, `Pull requests: write`).
GitLab additionally needs OAuth `api`; see [the upgrade steps](gitlab.md).
No new environment variable or external service is required.

## Slack usage

Ask naturally to create a request, for example:

> @nudgy owner/repo içindeki bu değişiklik için PR aç.

Nudgy finishes/publishes the task if needed, then calls
`repository_change_request_create` with its task ID, title and body. GitLab uses
`gitlab_repository_change_request_create`. Creation is limited to the task owner's
current Slack conversation, the published task branch and its original default
branch. It never runs tests or merges. The description explicitly says automated
tests were not run. Existing requests (including closed/merged ones) are returned
instead of creating another for the same task. The actual URL/state is returned.

For merge, send one of these explicit commands after mentioning Nudgy:

```text
merge https://github.com/owner/repo/pull/12
https://github.com/owner/repo/pull/12 merge et
merge https://gitlab.com/group/subgroup/project/-/merge_requests/34
https://gitlab.com/group/subgroup/project/-/merge_requests/34 birleştir
```

`birleştir <URL>` is also accepted. Slack-formatted links (`<URL|label>`) work.
The host matches the complete signed message after the bot mention is removed.
The provider, repository and request number must match the URL exactly. Bare
“merge et”, quoted examples, questions, negations, conditional instructions,
multiple commands or requests that only ask to code/publish/create/review do not
authorize a merge. The assistant explains the accepted command when ambiguous.
No second approval is required for a valid explicit command.

`repository_change_request_status` / `gitlab_repository_change_request_status`
return the provider's current state, source SHA, target branch, draft flag and
readiness. `repository_change_request_merge` / `gitlab_repository_change_request_merge`
use the original Slack message, never model-provided consent. Existing PRs/MRs
can be merged even if they were not created by Nudgy, subject to current access.

## Permissions and concurrency

Every operation rechecks Slack workspace ownership, token subject, current
repository access and, for GitLab, the selected project list. PR/MR creation uses
the existing encrypted edit row lock and saves returned request metadata there.
If the provider accepted creation but the response/database write was lost, the
next attempt searches by exact source/target branch before issuing another POST.
Source changes are detected against the published commit. No credentials or
provider error bodies are included in tool output.

The additive `009_merge_intents.sql` migration runs when coding stores initialize.
It pins provider, Slack owner/event/conversation, account, repository, request
number, head SHA and target branch before merging. Replays of that event cannot
authorize a different revision or target. After the PR/MR changes, a new explicit
Slack request is required. Already merged requests are reported as such without
another merge call, including after a lost response or job restart.

Both providers receive the pinned head SHA as an optimistic concurrency guard.
Immediately before the write, Nudgy rereads the request and checks its target and
head. GitHub must report `mergeable=true` and `mergeable_state=clean`; GitLab must
report `detailed_merge_status=mergeable`. Draft, closed, conflicting, blocked or
unknown readiness stops the operation. The remote provider also enforces its
current merge permissions and rules. Nudgy does not request rule bypass, force
push, approval overrides, source-branch deletion or delayed auto-merge.

GitHub currently uses the direct merge-commit REST endpoint (`merge_method=merge`).
Repositories requiring squash/rebase only, a merge queue, or stacked PR support
may reject this operation; Nudgy reports failure rather than changing strategy.
GitLab uses its project's configured merge strategy. Unknown/rejected API results
are never reported as successful merges. Target-branch checks are refreshed before
writing; provider APIs atomically guard the source SHA, not every concurrent edit
to PR/MR metadata.

This feature does not run CI or tests itself. Provider readiness reflects configured
repository rules; the model must not claim Nudgy ran tests or that every possible
test exists. Creation, status and merging remain separate actions.

## Validation

Run `go test -race ./...` and `go vet ./...`. Provider tests mock HTTP; they create
no real requests or merges. Cases cover explicit intent, wrong targets/providers,
identity injection, source changes, drafts/blocked status, lost-response recovery,
API errors, redirects and OAuth scope migration. With a disposable database in
`NUDGE_TEST_DATABASE_URL`, `TestCodingStorePostgres` additionally verifies durable
merge pins under concurrent requests, restart, user/workspace/provider isolation.

API references: [GitHub PR API](https://docs.github.com/en/rest/pulls/pulls),
[GitLab MR API](https://docs.gitlab.com/api/merge_requests/),
[GitLab OAuth scopes](https://docs.gitlab.com/integration/oauth_provider/).
