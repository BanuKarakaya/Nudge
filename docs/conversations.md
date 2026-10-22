# Contextual Slack conversations

App mentions are interpreted by the configured OpenAI-compatible model with the current request, the last 12
successful exchanges, and the articles linked in the most recent delivered RSS
summary/recommendation. Routing is model-based, not keyword-based. The action
enum describes supported backend data operations; ordinary conversation,
explanations and comparisons use a direct reply.

RSS intent is split by the requested output: `RSS_STATS` answers total unread
count and oldest unread date, while `RSS_CATEGORIES` answers which Feedbin
folders exist and how many unread entries each folder contains. Category counts
use the complete unread Feedbin inventory rather than the nightly 23:00 window.

## General assistance and web research

`WEB_SEARCH` is a backend capability, not a phrase users have to memorize. The
conversation model interprets topic, source preference and follow-up context.
For example, “RSS'lerimden değil, internetten AI agent hakkında bir makale bul”
routes to Google Search instead of reusing an unrelated Feedbin recommendation.
General explanations, writing help and conversation do not require Feedbin or
Raindrop accounts. The assistant must not claim unsupported actions were done.

Search runs in a separate Gemini `generateContent` request with `google_search` enabled,
without the JSON routing schema (for Gemini 2.5 compatibility). The core model handles
the assistant and data reasoning; the router creates
a self-contained public research request; private thread history and the RSS
inventory are not passed to the search call. This is a model instruction, not a
guarantee of automatic sensitive-data redaction; users should not send secrets.

Responses require search queries and valid web grounding sources. Only metadata
URLs are rendered as links; arbitrary generated URLs are not used. Source links
and the supplied Google search suggestion labels/targets appear with the answer
as native Slack links. Slack cannot render the provider's HTML/CSS search widget;
review Google's current display requirements before wider distribution. Search
errors, unsupported models, quotas and incomplete output produce an explicit
error, not an RSS fallback. Search does not compare a result against the user's
entire Feedbin inventory and must not promise that it is absent from that account.

When `OPENROUTER_API_KEY` is set, `OPENROUTER_MODEL` (default `openrouter/free`)
and `OPENROUTER_BASE_URL` (default `https://openrouter.ai/api/v1`) power basic
assistant requests, summaries and recommendations. The free model classifies
request complexity as part of its normal structured response. When it marks a
request as requiring substantial multi-step reasoning, the assistant and any
RSS summary/recommendation for that request are routed to
`OPENROUTER_COMPLEX_MODEL` (default `deepseek/deepseek-v4.1-flash`). This model
is usage-billed; OpenRouter may route it across multiple providers and prices
vary by provider. If the complex model is unavailable or returns an error,
Nudge retries the request with the free model and records the fallback in logs.
Basic requests do not invoke the complex model. OpenRouter is tried first so it
can be tested without changing the OpenAI configuration.
When `OPENAI_API_KEY` is set,
`OPENAI_MODEL` (default `gpt-6-luna`) and
`OPENAI_BASE_URL` (default `https://api.openai.com/v1`) power the core assistant,
summaries and recommendations. This is the paid OpenAI API path; usage is billed
by input and output tokens. If `OPENAI_API_KEY` is absent, the application falls
back to OpenCode and then direct DeepSeek according to the existing variables.
`GEMINI_API_KEY` and `GEMINI_MODEL`
remain optional and are used only for live Google Search because DeepSeek's API
does not provide Google's built-in Search grounding. Use a model supporting Google Search;
`gemini-2.5-flash` supports it. Google's pricing page currently lists up to 500
grounded requests/day on that model's free tier, shared with Flash-Lite, subject
to other project/model quotas. Paid-tier token costs and grounding overages can
apply when billing is enabled; this application does not enforce a spend cap.

References (checked 2026-10-12):

- https://ai.google.dev/gemini-api/docs/generate-content/google-search
- https://ai.google.dev/gemini-api/docs/pricing#gemini-2.5-flash
- https://ai.google.dev/gemini-api/terms#grounding-with-google-search

After deployment, test in Slack:

1. Ask for an RSS summary.
2. Ask “RSS'lerimden değil, internetten AI agent mimarileri hakkında bir makale
   öner; konusunu ve neden önemli olduğunu kısaca anlat.”
3. Check that the answer is relevant to agents and has web source links, not
   another RSS digest.
4. Ask “Bunu daha basit anlatır mısın?” in the same thread.

Mocked tests cover tool configuration, routing dispatch, history propagation,
source/suggestion rendering, invalid URLs, API quota errors and truncation.
Live intent accuracy and the account's actual search quota require this Slack test.

For a selection from an earlier summary, the core model returns an article ID and
a reason. Nudge verifies that ID against the delivered recommendations and renders
the stored title/URL itself. It does not fetch a new Feedbin inventory for that
follow-up. Invalid or unavailable model output produces an explicit failure
message, never a silent replacement with titles or another summary.

History is stored in Postgres, keyed by workspace, channel, thread and Slack user.
Only successfully sent replies are remembered. The same user's requests in one
thread are serialized within each server process. Different replicas are not
serialized by this lock. Conversation text is bounded to 3,000 characters per
request and 10,000 per reply; the last RSS recommendations are retained separately.
Conversation rows currently have no automatic retention expiry.

## Deployment and test

The server idempotently creates `nudge_conversations` from embedded migration
`004_conversations.sql` on startup. The database role needs CREATE permission;
otherwise an administrator must apply the migration before deployment. Existing
tables are not changed. No Slack history scopes are needed. For the OpenAI path,
configure `OPENAI_API_KEY` in Railway; the existing Gemini key/model configuration
is still used for live Google Search.

Old Slack messages are not imported, and scheduled DM digests are not currently
seeded into mention history. After deployment, ask for a new RSS summary with an
app mention, then continue in that same thread as the same user:

1. `@Nudge bugünkü RSS özetimi verir misin?`
2. `@Nudge teşekkür ederim`
3. `@Nudge o özetten yalnız birini okuyacak olsan hangisini seçerdin?`
4. `@Nudge neden onu seçtin, daha basit anlatır mısın?`

Step 3 should return one of the earlier linked articles and a reason, not a new
summary. A different thread or user does not inherit that private history.

Automated tests mock the AI provider and Slack, exercising context propagation, validated
selection, persistence across handler recreation, scope separation and failed
delivery. They do not certify live model intent accuracy or a production database
migration; run the above Slack check after deployment.
