CREATE TABLE IF NOT EXISTS nudge_slack_jobs (
    id BIGSERIAL PRIMARY KEY,
    team_id TEXT NOT NULL,
    event_key TEXT NOT NULL,
    channel_id TEXT NOT NULL,
    thread_ts TEXT NOT NULL,
    slack_user_id TEXT NOT NULL,
    payload JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','completed','failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_until TIMESTAMPTZ,
    claim_token TEXT,
    result JSONB,
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    UNIQUE (team_id, event_key)
);

CREATE INDEX IF NOT EXISTS nudge_slack_jobs_pending
    ON nudge_slack_jobs (available_at, id) WHERE status IN ('queued','running');
CREATE INDEX IF NOT EXISTS nudge_slack_jobs_conversation
    ON nudge_slack_jobs (team_id,channel_id,thread_ts,slack_user_id,id)
    WHERE status IN ('queued','running');
