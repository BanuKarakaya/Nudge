# AI analytics dashboard

Nudge exposes the private dashboard at `/dashboard` and its data endpoint at
`/api/dashboard`. Each Slack mention is stored in `nudge_ai_interactions` with
the request, response, action, selected model(s), provider(s), end-to-end
latency, prompt tokens, completion tokens and status.

Set `NUDGE_DASHBOARD_TOKEN` in production. The dashboard asks for this value
when the API returns `401`; it is kept in the browser's local storage and sent
as `X-Dashboard-Token`.

The analytics migration is applied idempotently when the server starts. Provider
usage fields are recorded when the provider returns usage metadata; latency and
model data are still recorded when a provider omits token counts.
