package postgres

import (
	"context"
	"encoding/json"
	"nudge/internal/analytics"
	"nudge/internal/repository"
	"regexp"
	"time"
)

var safeLogID = regexp.MustCompile(`^[a-zA-Z0-9_./:@+ -]{0,200}$`)
var safeErrorCode = regexp.MustCompile(`^[a-z_]{0,80}$`)

func (r *SlackJobRepository) ReportJob(ctx context.Context, j repository.SlackJob, e analytics.LiveEvent) error {
	if !analytics.ValidStage(e.Stage) {
		return nil
	}
	if !safeLogID.MatchString(e.Model) {
		e.Model = ""
	}
	if !safeLogID.MatchString(e.Tool) {
		e.Tool = ""
	}
	if !safeErrorCode.MatchString(e.ErrorCode) {
		e.ErrorCode = "operation_failed"
	}
	if e.DurationMS < 0 {
		e.DurationMS = 0
	}
	raw, err := json.Marshal(repository.MonitorEvent{LiveEvent: e, At: time.Now().UTC(), Attempt: j.Attempts})
	if err != nil {
		return err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = jobUpdated(tx.Exec(ctx, `UPDATE nudge_slack_jobs SET
 stage=CASE WHEN $3 IN ('tool_completed','tool_failed') AND stage='tool_started' THEN 'model_running' WHEN $3 IN ('usage','attempt_failed','tool_completed','tool_failed') THEN stage ELSE $3 END,
 current_model=CASE WHEN $4='' THEN current_model ELSE $4 END,
 current_tool=CASE WHEN $3='tool_started' THEN $5 WHEN $3 IN ('tool_completed','tool_failed','model_running') THEN '' ELSE current_tool END,
 had_error=had_error OR $6 NOT IN ('','none'),
 error_code=CASE WHEN $6 IN ('','none') OR ($3='attempt_failed' AND error_code<>'') THEN error_code ELSE $6 END,
 trace=(SELECT COALESCE(jsonb_agg(value ORDER BY ord),'[]'::jsonb) FROM jsonb_array_elements(trace || $7::jsonb) WITH ORDINALITY AS e(value,ord) WHERE ord>GREATEST(jsonb_array_length(trace)+1-128,0)),
 tool_failure_count=tool_failure_count+CASE WHEN $3 IN ('tool_failed','action_failed') THEN 1 ELSE 0 END,
 trace_count=trace_count+1,updated_at=NOW()
 WHERE `+ownedSlackJob, j.ID, j.ClaimToken, e.Stage, e.Model, e.Tool, e.ErrorCode, raw)); err != nil {
		return err
	}
	if u := e.Usage; u != nil && safeLogID.MatchString(u.Model) && safeLogID.MatchString(u.Provider) && u.Model != "" && u.PromptTokens >= 0 && u.CompletionTokens >= 0 && u.TotalTokens >= 0 {
		_, err = tx.Exec(ctx, `INSERT INTO nudge_job_model_usage(job_id,provider,model,calls,prompt_tokens,completion_tokens,total_tokens,duration_ms,cached_tokens,reasoning_tokens,cost_usd,cost_calls) VALUES($1,$2,$3,1,$4,$5,$6,$7,$8,$9,COALESCE($10::double precision,0),CASE WHEN $10::double precision IS NULL THEN 0 ELSE 1 END)
 ON CONFLICT(job_id,provider,model) DO UPDATE SET calls=nudge_job_model_usage.calls+1,prompt_tokens=nudge_job_model_usage.prompt_tokens+EXCLUDED.prompt_tokens,completion_tokens=nudge_job_model_usage.completion_tokens+EXCLUDED.completion_tokens,total_tokens=nudge_job_model_usage.total_tokens+EXCLUDED.total_tokens,duration_ms=nudge_job_model_usage.duration_ms+EXCLUDED.duration_ms,cached_tokens=nudge_job_model_usage.cached_tokens+EXCLUDED.cached_tokens,reasoning_tokens=nudge_job_model_usage.reasoning_tokens+EXCLUDED.reasoning_tokens,cost_usd=nudge_job_model_usage.cost_usd+EXCLUDED.cost_usd,cost_calls=nudge_job_model_usage.cost_calls+EXCLUDED.cost_calls`, j.ID, u.Provider, u.Model, u.PromptTokens, u.CompletionTokens, u.TotalTokens, max(u.LatencyMS, 0), min(max(u.CachedTokens, 0), u.PromptTokens), max(u.ReasoningTokens, 0), u.CostUSD)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
