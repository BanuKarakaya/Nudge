-- Preserve tool failures even when the response is delivered or the trace rolls over.
ALTER TABLE nudge_slack_jobs ADD COLUMN tool_failure_count BIGINT NOT NULL DEFAULT 0;
UPDATE nudge_slack_jobs j SET tool_failure_count=(
 SELECT COUNT(*) FROM jsonb_array_elements(j.trace) e
 WHERE e->>'stage' IN ('tool_failed','action_failed')
);
