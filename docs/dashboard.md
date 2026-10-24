# AI analytics dashboard

Nudge exposes the private dashboard at `/dashboard` and its data endpoint at
`/api/dashboard`. Each Slack mention is stored in `nudge_ai_interactions` with
the request, response, action, selected model(s), provider(s), end-to-end
latency, prompt tokens, completion tokens and status.

Set `NUDGE_DASHBOARD_TOKEN` to an independent, high-entropy secret. If it is
missing or blank, the data API returns `503` and never exposes analytics.
Wrong or missing credentials return `401`. The public HTML shell contains no
conversation data.

Enter the token in the dashboard. It is held only in page memory and sent as
`X-Dashboard-Token`; reloading requires entering it again. Existing tokens from
older versions are removed from local storage. API clients may also use
`Authorization: Bearer <token>`. Query-string tokens are not accepted.
Responses use `Cache-Control: no-store` and `Referrer-Policy: no-referrer`.
Use HTTPS outside local development. This is a single administrator dashboard
covering all workspaces, not a user-scoped login. Never share its token with
ordinary workspace members.

The analytics migration is applied idempotently when the server starts. Provider
usage fields are recorded when the provider returns usage metadata; latency and
model data are still recorded when a provider omits token counts.

The sidebar links and summary cards navigate to the corresponding sections.
Click a conversation's message to read the complete request/response, model,
provider, status, timestamp, latency, and token breakdown. Model legend buttons
and the model details dialog apply a model filter. Model and status filters
apply to both summary metrics and conversations; clearing them retains the date
range. Notifications summarize failed/degraded interactions in the current view
and link to those records; they are not a Slack notification inbox.

The API accepts `limit` (1–100, default 24) and `offset` (non-negative, default 0).
Summary totals are independent of pagination. Conversations sort by creation
time and ID descending. “Tümünü gör” switches to 100-record pages; Previous/Next
reach the remaining records without discarding filters. Status `issues` includes
both `failed` and `degraded` records. Invalid pagination returns HTTP 400.

The workspace control explains the all-workspaces scope. The account control
lets the administrator change the in-memory token or sign out. Sign-out and
failed requests clear previously displayed records and details; outdated
requests cannot restore them. Connection indicators reflect the last dashboard
API request, not Slack's overall service health. The mobile layout exposes the
same token entry and account controls.

Checks: `node --test internal/dashboard/app_test.cjs` and
`go test ./internal/handler ./internal/repository/postgres ./internal/dashboard`.

## Operations page

`/dashboard#operations` shows the separate **Yapılan işlemler** page. It shares
date/model/message-status filters and pagination with Overview. Each request
opens a timeline of actual tool calls, not actions inferred from assistant text.
Tool status is distinct from the overall message/delivery status: a tool can
fail even if the agent subsequently delivers a successful explanation.

The Codex adapter records RSS inventory, article retrieval, bookmarks, web search
and model handoff attempts, including failure/rejection, model, start time and
duration. Replayed tool call IDs do not create duplicate steps. Safe numerical
RSS/bookmark result counts and pagination are retained; raw arguments, article
bodies, titles, URLs, search results, provider errors and credentials are not.
The technical-log expander shows structured metadata, not process stdout.

Trace data is attached to its Slack interaction and saved on request completion,
including failed turns, using the existing bounded analytics persistence call.
It is **not** a durable live audit stream: hard crashes can lose in-flight traces,
and persistence failures remain in server logs. Scheduled background digests
are not included. Use Refresh to fetch newly completed requests.

Startup applies additive `operations` JSONB and `trace_version` columns without
deleting existing rows. Historical rows are explicitly marked as lacking trace
data. A new request with no tool calls is not labelled as a successful tool action.
No new environment variable is required. The existing administrator token
protects all operation details, across all workspaces.

The bounded Operation schema and detail renderer support `path`/`diff` for future
write adapters. Those adapters must only emit confirmed actions with reviewed,
secret-redacted diffs. **No GitHub/GitLab connection, file editing or PR/MR creation
is implemented by this dashboard change.** Unknown tools only record metadata.

Tests: `go test -race ./internal/analytics ./internal/codex ./internal/handler`,
and `NUDGE_TEST_DATABASE_URL=<disposable-db> go test ./internal/repository/postgres
-run TestAnalyticsOperationsPostgres` verifies upgrade, repeatable migration,
trace upsert, filters and pagination in an isolated test schema.
