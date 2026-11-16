ALTER TABLE nudge_slack_jobs ADD COLUMN stage TEXT NOT NULL DEFAULT 'queued';
ALTER TABLE nudge_slack_jobs ADD COLUMN current_model TEXT NOT NULL DEFAULT '';
ALTER TABLE nudge_slack_jobs ADD COLUMN current_tool TEXT NOT NULL DEFAULT '';
ALTER TABLE nudge_slack_jobs ADD COLUMN error_code TEXT NOT NULL DEFAULT '';
ALTER TABLE nudge_slack_jobs ADD COLUMN trace JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE nudge_slack_jobs ADD COLUMN trace_count INTEGER NOT NULL DEFAULT 0;
CREATE INDEX nudge_slack_jobs_dashboard ON nudge_slack_jobs(team_id,created_at DESC,id DESC);
CREATE INDEX nudge_slack_jobs_request_lookup ON nudge_slack_jobs(team_id,slack_user_id,('slack-job-'||id::text));
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN profile_checked_at TIMESTAMPTZ;
CREATE TABLE nudge_job_model_usage (
 job_id BIGINT NOT NULL REFERENCES nudge_slack_jobs(id) ON DELETE CASCADE,
 provider TEXT NOT NULL,
 model TEXT NOT NULL,
 calls BIGINT NOT NULL DEFAULT 0,
 prompt_tokens BIGINT NOT NULL DEFAULT 0,
 completion_tokens BIGINT NOT NULL DEFAULT 0,
 total_tokens BIGINT NOT NULL DEFAULT 0,
 duration_ms BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY(job_id,provider,model)
);

-- Existing queue rows retain the state that can already be verified; do not
-- fabricate historical event traces or provider token splits.
UPDATE nudge_slack_jobs SET stage=CASE
 WHEN delivered_at IS NOT NULL THEN 'delivery_completed'
 WHEN result IS NOT NULL THEN 'response_ready'
 WHEN status='running' THEN 'worker_started'
 ELSE 'queued' END;
