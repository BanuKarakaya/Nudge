ALTER TABLE nudge_job_model_usage ADD COLUMN cached_tokens BIGINT NOT NULL DEFAULT 0;
ALTER TABLE nudge_job_model_usage ADD COLUMN reasoning_tokens BIGINT NOT NULL DEFAULT 0;
ALTER TABLE nudge_job_model_usage ADD COLUMN cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE nudge_job_model_usage ADD COLUMN cost_calls BIGINT NOT NULL DEFAULT 0;
CREATE TABLE nudge_model_calls (
 id BIGSERIAL PRIMARY KEY,
 job_id BIGINT NOT NULL REFERENCES nudge_slack_jobs(id) ON DELETE CASCADE,
 attempt INTEGER NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 metadata JSONB NOT NULL,
 contents TEXT NOT NULL DEFAULT ''
);
CREATE INDEX nudge_model_calls_job ON nudge_model_calls(job_id,id);
CREATE TABLE nudge_http_errors (
 id BIGSERIAL PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 request_id TEXT NOT NULL,
 method TEXT NOT NULL,
 route TEXT NOT NULL,
 status INTEGER NOT NULL,
 duration_ms BIGINT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX nudge_http_errors_user ON nudge_http_errors(user_id,created_at DESC);
ALTER TABLE nudge_slack_jobs ADD COLUMN had_error BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE nudge_slack_jobs SET had_error=TRUE WHERE status='failed' OR tool_failure_count>0 OR EXISTS (
 SELECT 1 FROM jsonb_array_elements(trace) event WHERE COALESCE(event->>'error_code','') NOT IN ('','none')
);
ALTER TABLE nudge_slack_jobs ADD COLUMN model_call_count BIGINT NOT NULL DEFAULT 0;
