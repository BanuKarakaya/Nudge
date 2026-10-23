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
