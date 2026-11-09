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

### Natural-language merging

The current user can request merging in their own words. There is no required
command template or need to repeat a PR/MR URL already clear in the conversation:

```text
@nudgy bunu mergele
@nudgy bu PR ı merge et
@nudgy mergeleme işlemini tekrar dener misin
@nudgy bu branchi development branchine alabilir misin
@nudgy could you merge this PR please?
```

An explicit URL also works. Reference resolution uses only this user's delivered
conversation in this Slack thread, not other users' Slack messages or repository
instructions. A retry can authorize another merge attempt when the preceding
operation was a merge. A retry of creation/review, a status question, negation,
quoted example, conditional/deferred request or a mere acknowledgement does not
provide merge authority. When the intended PR/MR or branch direction is ambiguous,
Nudgy asks a short clarifying question instead of prescribing a fixed command.

`repository_change_request_status` / `gitlab_repository_change_request_status`
verify the real PR/MR, then `repository_change_request_merge` (or its GitLab
variant) attempts the requested merge. Requests may predate Nudgy. For a source
and target branch, `repository_branches_merge` / `gitlab_repository_branches_merge`
finds or creates the PR/MR and attempts the merge. Arbitrary existing branches
are supported, not just Nudgy task branches; no PR URL is needed. The direction
must be clear, branch case is preserved, and a default target is never inferred.
A request to change the destination uses branch mode, not an older PR's target.

### Host intent boundary

On the first merge tool invocation in an event, the host starts a fresh, tool-free
intent-resolution turn using the configured simple model. It receives the exact
current user message and delivered owner-bound history, independently of the
executing assistant's proposed arguments, native thread and tool results. It
returns `merge`, `none`, or `clarify`, plus one concrete provider/repository and
PR/MR number or source/target pair. The host validates the shape, checks that the
identifiers occur in the supplied context, and requires an exact match with the
merge tool arguments. A main-agent `authorized` flag cannot grant permission.
Language interpretation is model-based; identifier, access and revision checks
remain deterministic.

The decision is cached for the current processing attempt and shared by both
providers. Resolution has a separate bounded concurrency pool and a 30-second
limit, avoiding deadlock when the main agent is waiting for authorization. It
adds one model call only when merging is attempted. A timeout or malformed answer
stops the write without falling back to a permissive command check. Resolved
intent is not a durable approval; the existing database merge pins continue to
protect retries against changed revisions/targets. Non-Slack callers without a
host resolver retain the conservative legacy explicit-command parser.

Existing native conversations are restarted once with delivered history when the
new tool version is loaded, so old fixed-command instructions do not persist.
No new environment variable, migration or provider permission is required.

The runtime reports only safe error categories (intent mismatch/unavailable,
changed revision, blocked checks, missing GitLab scope), never raw diagnostics.
Intent failures must not be presented as confirmed provider permission failures.
If provider checks block a branch merge, the actual PR/MR is returned. Pending
mergeability gets a short synchronous recheck; no deferred auto-merge is enabled.
Success is reported only when the provider confirms `state=merged`.

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
no real requests or merges. The model interpreter is stubbed in offline tests; they verify isolation, target
grounding, host authorization and transport, not live-model language accuracy.
Cases cover contextual intent, wrong targets/providers,
identity injection, source changes, drafts/blocked status, lost-response recovery,
API errors, redirects and OAuth scope migration. With a disposable database in
`NUDGE_TEST_DATABASE_URL`, `TestCodingStorePostgres` additionally verifies durable
merge pins under concurrent requests, restart, user/workspace/provider isolation.

API references: [GitHub PR API](https://docs.github.com/en/rest/pulls/pulls),
[GitLab MR API](https://docs.gitlab.com/api/merge_requests/),
[GitLab OAuth scopes](https://docs.gitlab.com/integration/oauth_provider/).
