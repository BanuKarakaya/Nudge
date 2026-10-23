CREATE TABLE IF NOT EXISTS nudge_ai_interactions (
    id BIGSERIAL PRIMARY KEY,
    request_id TEXT NOT NULL UNIQUE,
    team_id TEXT NOT NULL,
    channel_id TEXT NOT NULL,
    thread_ts TEXT NOT NULL,
    slack_user_id TEXT NOT NULL,
    user_id BIGINT,
    request_text TEXT NOT NULL,
    response_text TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'processing',
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    latency_ms BIGINT NOT NULL DEFAULT 0,
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    total_tokens BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS nudge_ai_interactions_created_at_idx
    ON nudge_ai_interactions (created_at DESC);

CREATE INDEX IF NOT EXISTS nudge_ai_interactions_model_idx
    ON nudge_ai_interactions (model);
