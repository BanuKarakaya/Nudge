# Durable Slack request lifecycle

Signed `app_mention` events are persisted in PostgreSQL `nudge_slack_jobs` before
the webhook returns HTTP 200. Admission has a two-second deadline. If persistence
fails (or the queue is unavailable), HTTP 503 lets Slack retry; the handler never
starts a best-effort background goroutine. Other messages are ignored, and Slack
URL verification still requires a valid signature without creating a job.

The embedded `008_slack_jobs.sql` migration runs at server startup. It is additive
and rerunnable, needs CREATE privileges, and does not replay historical messages.
No broker, new credentials, or additional service is needed. The existing database
backup must include this table. Stop old application instances during the first
upgrade: old instances acknowledge requests without inserting durable jobs.

## Execution and recovery

Four worker loops poll at one-second intervals. PostgreSQL atomically claims work
using `FOR UPDATE SKIP LOCKED`. Only the oldest unfinished job for each
workspace/channel/thread/user tuple is eligible, including while that job is
backing off. Unrelated conversations can run concurrently. Events deduplicate
per workspace and Slack event ID; channel/message timestamp is the fallback when
the event ID is absent. Duplicate deliveries never overwrite the original payload,
including after completion or restart.

States are `queued`, `running`, `completed`, and `failed`. A worker has a random
claim token and a four-minute lease. The whole attempt has a three-minute context
deadline, shorter than the lease. Every tool invocation checks ownership, and
checkpoint/delivery/completion/retry mutations reject expired or superseded claims.
Startup never clears another worker's active claim. A hard crash becomes eligible
for automatic recovery when its lease expires (at most four minutes after claim).
SIGTERM/SIGINT cancels workers, then releases interrupted claims immediately after
processing stops. If shutdown itself is interrupted, lease expiry recovers them.

The request's original authenticated identity and event key are retained on every
attempt. If generation was interrupted, the runtime rebuilds from delivered
conversation history instead of reusing a potentially half-written native thread.
The recovery input tells it to discover and inspect saved repository tasks before
continuing. Existing repository edits persist separately and retain their original
event identity and idempotent branch publishing behavior. This resumes the request
from durable state; it does not resume an LLM subprocess at the exact interrupted
token, and interrupted model calls may incur usage again.

## Response checkpoint and delivery

Before sending a final reply, the worker saves its text, host-observed coding review
metadata, intended next history, and analytics record. Expiring signed buttons are
generated at delivery time, never persisted in the checkpoint. A retry with a
saved response bypasses the model and tools entirely, and resolves current Slack
credentials and membership before delivery.

After Slack confirms delivery, `delivered_at` is persisted. Finalization atomically
writes conversation history, analytics, and the terminal job status. A crash after
the delivery mark only repeats finalization; it does not resend or append another
history exchange. Handled model errors deliver the existing failure message and
finish as `failed` (analytics `degraded`). Infrastructure or delivery errors keep
the job queued, retrying after 5, 10, 20, 40, 80, 160 seconds, capped at five minutes.
These retries have no fixed attempt limit; a permanently broken connection can
block later requests for that conversation and needs operational repair.

Delivery is **at least once**. Slack delivery and the database mark cannot commit
atomically: if Slack accepted a message but the response or delivery mark was lost,
the same saved reply can be sent again. Recovery before the response checkpoint can
repeat model calls and tools; repository mutations retain their existing idempotency
checks, but arbitrary external effects (for example connection-link DMs) do not have
a universal exactly-once guarantee.

The job payload/checkpoint contains conversation content, like existing history and
analytics. It must be treated as private application data. It is never exposed by a
public queue endpoint. Bot tokens, OAuth tokens, and generated bearer links are not
stored in these records. Completed job identities are retained for deduplication;
there is no automatic retention cleanup in this change.

The queue coordinates claims across instances, but this does not provision shared
Codex state or make separate filesystem volumes interchangeable. Keep the existing
single-replica deployment until runtime storage has been configured for scaling.
The dashboard continues to show finalized interactions; this change does not add a
live queue UI. Pending work can be inspected without message content using:

```sql
SELECT id, status, attempts, available_at, lease_until, delivered_at,
       result IS NOT NULL AS response_ready, created_at, updated_at
FROM nudge_slack_jobs
WHERE status IN ('queued', 'running')
ORDER BY id;
```

## Verification

`go test -race ./...` and `go vet ./...` cover admission failure, shutdown, replay,
response checkpoint recovery, and delivery without rerunning the agent. With
`NUDGE_TEST_DATABASE_URL` set to a disposable PostgreSQL database, run
`go test -race ./internal/repository/postgres -run TestSlackJobsPostgres` for
concurrent claims, conversation ordering, deduplication, migration repeatability,
expired/stale claim fencing, restart recovery, and atomic finalization. Integration
tests create and remove an isolated schema. No live Slack or model calls are used.
