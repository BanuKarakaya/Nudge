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
