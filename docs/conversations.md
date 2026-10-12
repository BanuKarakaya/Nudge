# Contextual Slack conversations

App mentions are interpreted by Gemini with the current request, the last 12
successful exchanges, and the articles linked in the most recent delivered RSS
summary/recommendation. Routing is model-based, not keyword-based. The action
enum describes supported backend data operations; ordinary conversation,
explanations and comparisons use a direct reply.

For a selection from an earlier summary, Gemini returns an article ID and a
reason. Nudge verifies that ID against the delivered recommendations and renders
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
tables are not changed. No new environment variables or Slack history scopes
are needed; the existing Gemini key/model configuration is used.

Old Slack messages are not imported, and scheduled DM digests are not currently
seeded into mention history. After deployment, ask for a new RSS summary with an
app mention, then continue in that same thread as the same user:

1. `@Nudge bugünkü RSS özetimi verir misin?`
2. `@Nudge teşekkür ederim`
3. `@Nudge o özetten yalnız birini okuyacak olsan hangisini seçerdin?`
4. `@Nudge neden onu seçtin, daha basit anlatır mısın?`

Step 3 should return one of the earlier linked articles and a reason, not a new
summary. A different thread or user does not inherit that private history.

Automated tests mock Gemini and Slack, exercising context propagation, validated
selection, persistence across handler recreation, scope separation and failed
delivery. They do not certify live model intent accuracy or a production database
migration; run the above Slack check after deployment.
