CREATE TABLE IF NOT EXISTS nudge_merge_intents (
    provider TEXT NOT NULL CHECK (provider IN ('github','gitlab')),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    channel_id TEXT NOT NULL,
    thread_ts TEXT NOT NULL,
    pin JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(provider,user_id,team_id,event_id)
);
