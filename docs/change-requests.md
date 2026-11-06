# GitHub PR and GitLab MR operations

Nudgy can create a pull/merge request between any two existing branches in one
currently authorized repository, or from a published coding task. An explicit
branch merge request can create/find the PR/MR and merge it in the same operation.
GitHub uses the existing App user token (`Contents: write`, `Pull requests: write`).
GitLab additionally needs OAuth `api`; see [the upgrade steps](gitlab.md).
No new environment variable or external service is required.

## Slack usage

Ask naturally to create a request, for example:

> @nudgy owner/repo içindeki bu değişiklik için PR aç.

Nudgy finishes/publishes the task if needed, then calls
`repository_change_request_create` with its task ID, title, body and optional
`target_branch`. GitLab uses `gitlab_repository_change_request_create`. The task
must belong to the current user and Slack conversation. The target may be any
existing branch; omitting it uses the task's original default branch.
It never runs tests or merges. The description explicitly says automated
tests were not run. Existing requests (including closed/merged ones) are returned
instead of creating another for the same task. The actual URL/state is returned.

For branches not created by Nudgy, use `repository_branch_change_request_create`
with `repository`, `source_branch`, `target_branch`, `title`, `body`. GitLab uses
the `gitlab_` prefix. Discover exact names with `repository_branches_list`
(`page=1`, 100 per page; follow `next_page` until null). The source and target
must exist in the same repository and must differ. A previously merged branch
with new source commits can produce a new request. Creation never merges.

### Merge by branch

```text
@nudgy bu dalı development dalına merge et
@nudgy tamam bence olmuş bu branchi development branch ine mergeleyebilir misin
@nudgy github owner/repo feature/login -> development merge et
@nudgy gitlab group/subgroup/project release/v2 -> main merge et
```

`repository_branches_merge` (or its `gitlab_` variant) finds/creates the request
for the selected pair and then attempts the merge. No PR/MR URL is required.
For arbitrary branches the signed message must explicitly name provider,
repository, source and target as above. The host checks these against tool
arguments; the model cannot replace the target or invent consent.
“Bu dal” is accepted only when the current user's thread has exactly one coding
task, already published, and no competing task on the other configured provider.
With multiple tasks, specify the full source/target command. Branch case is
preserved. Negations, quoted instructions, conditions and compound commands
do not authorize merging.

Slack inline-code formatting is accepted around a whole command, URL or branch
name. For example, a copied `<PR URL> merge et` command remains valid when Slack
wraps it in backticks. Fenced blocks, blockquotes and example prose remain invalid.
`source-branch bu branchi development branchine mergeleyebilir misin` also works
when that source identifies exactly one published task in the current user's
thread. A different source supplied by the model is rejected. If discovery is
truncated or the other provider has competing tasks, use the fully specified
provider/repository/source/target command.

The runtime reports whitelisted merge failure reasons (intent mismatch, changed
revision, blocked checks, missing GitLab scope) to the assistant and logs only a
safe error code. Unknown errors still hide raw provider/network/database details;
they must not be presented as confirmed branch protection or permission failures.

The operation returns the actual PR/MR if checks block merging. Pending provider
mergeability calculations get a short bounded synchronous recheck; Nudgy never
enables deferred auto-merge. A failed or unknown result is not reported as merged.

### Merge by PR/MR URL

For an existing PR/MR, send one of these explicit commands after mentioning Nudgy:

```text
merge https://github.com/owner/repo/pull/12
https://github.com/owner/repo/pull/12 merge et
merge https://gitlab.com/group/subgroup/project/-/merge_requests/34
https://gitlab.com/group/subgroup/project/-/merge_requests/34 birleştir
```

`birleştir <URL>` is also accepted. Slack-formatted links (`<URL|label>`) work.
The host matches the complete signed message after the bot mention is removed.
The provider, repository and request number must match the URL exactly. Bare
“merge et”, quoted examples, negations, conditional instructions,
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
repository access and, for GitLab, the selected project list. Task PR/MR creation uses
the existing encrypted edit row lock and saves returned request metadata there.
If the provider accepted creation but the response/database write was lost, the
next attempt searches by exact source/target branch before issuing another POST.
Task source changes are detected against the published commit; arbitrary branch
creation checks the live source revision. No credentials or
provider error bodies are included in tool output.

The additive `009_merge_intents.sql` migration runs when coding stores initialize.
It pins provider, Slack owner/event/conversation, account, repository, request
number, head SHA and target branch before merging. Replays of that event cannot
authorize a different revision or target. After the PR/MR changes, a new explicit
Slack request is required. Already merged requests are reported as such without
another merge call, including after a lost response or job restart.
Branch merges additionally save the source branch and operation mode in the same
JSON pin. Retries retrieve that exact PR/MR instead of opening another, even if
the source branch was deleted after merging. This is an additive JSON extension;
no new migration or environment setting is required.

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
test exists. Creation-only requests never authorize a merge; the combined branch
merge tool requires an explicit merge request.

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
