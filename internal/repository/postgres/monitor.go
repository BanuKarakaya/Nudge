package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"nudge/internal/repository"
	"nudge/internal/workspace"
	"time"
)

// The projection never selects payload/result, message text, credentials or code.
// Jobs are counted even if they never reached analytics completion. Historical
// interactions without a retained job remain visible without double counting.
const monitorCTE = `WITH activity AS (
 SELECT j.id,j.team_id,j.slack_user_id,j.channel_id,j.thread_ts,j.status,j.stage,j.current_tool,j.error_code,j.attempts,j.trace_count,
 j.created_at,j.updated_at,j.available_at,j.lease_until,j.finished_at,j.delivered_at,j.result IS NOT NULL AS response_ready,
 COALESCE(u.models,NULLIF(a.model,''),j.current_model,'') AS model,
 COALESCE(u.prompt,a.prompt_tokens,0)::bigint AS prompt_tokens,COALESCE(u.completion,a.completion_tokens,0)::bigint AS completion_tokens,COALESCE(u.tokens,a.total_tokens,0)::bigint AS total_tokens,
 GREATEST(0,EXTRACT(EPOCH FROM (COALESCE(j.finished_at,NOW())-j.created_at))*1000)::bigint AS latency_ms,j.trace,j.tool_failure_count
 FROM nudge_slack_jobs j LEFT JOIN nudge_ai_interactions a ON a.request_id='slack-job-'||j.id AND a.team_id=j.team_id AND a.slack_user_id=j.slack_user_id
 LEFT JOIN LATERAL (SELECT SUM(prompt_tokens) prompt,SUM(completion_tokens) completion,SUM(total_tokens) tokens,string_agg(DISTINCT model,' + ' ORDER BY model) models FROM nudge_job_model_usage WHERE job_id=j.id) u ON TRUE
 WHERE j.team_id=$1 AND j.created_at >= $2 AND j.created_at < $3
 UNION ALL
 SELECT -a.id,a.team_id,a.slack_user_id,a.channel_id,a.thread_ts,CASE WHEN a.status='success' THEN 'completed' WHEN a.status='processing' THEN 'running' ELSE 'failed' END,'legacy','','',0,0,
 a.created_at,a.created_at,NULL::timestamptz,NULL::timestamptz,CASE WHEN a.status<>'processing' THEN a.created_at END,NULL::timestamptz,FALSE,a.model,a.prompt_tokens,a.completion_tokens,a.total_tokens,a.latency_ms,'[]'::jsonb,0::bigint
 FROM nudge_ai_interactions a WHERE a.team_id=$1 AND a.created_at >= $2 AND a.created_at < $3
 AND ($7 OR a.user_id IS NULL OR a.user_id=$6)
 AND NOT EXISTS(SELECT 1 FROM nudge_slack_jobs j WHERE a.request_id='slack-job-'||j.id AND j.team_id=a.team_id AND j.slack_user_id=a.slack_user_id)
), scoped AS (SELECT * FROM activity WHERE ($4='' OR slack_user_id=$4) AND ($5='' OR model ILIKE '%'||$5||'%')) `

func (r *AnalyticsRepository) Monitor(ctx context.Context, user int64, teamScope bool, f repository.MonitorFilters) (repository.MonitorSummary, error) {
	var out repository.MonitorSummary
	var team, slackUser string
	var manager bool
	err := r.db.QueryRow(ctx, `SELECT u.team_id,u.slack_user_id,COALESCE(i.installer_slack_user_id=u.slack_user_id,FALSE) FROM users u JOIN slack_installations i ON i.team_id=u.team_id AND i.bot_token<>'' WHERE u.id=$1`, user).Scan(&team, &slackUser, &manager)
	if err != nil || (teamScope && !manager) {
		return out, workspace.ErrDenied
	}
	if f.To.IsZero() {
		f.To = time.Now()
	}
	if f.From.IsZero() {
		f.From = f.To.Add(-7 * 24 * time.Hour)
	}
	if !teamScope {
		f.User = slackUser
	}
	args := []any{team, f.From, f.To, f.User, f.Model, user, teamScope}
	err = r.db.QueryRow(ctx, monitorCTE+`SELECT COUNT(*) FILTER(WHERE status='failed' OR tool_failure_count>0),COUNT(*) FILTER(WHERE tool_failure_count>0),COUNT(*),COUNT(*) FILTER(WHERE status='queued'),COUNT(*) FILTER(WHERE status='running'),COUNT(*) FILTER(WHERE status='completed'),COUNT(*) FILTER(WHERE status='failed'),COUNT(*) FILTER(WHERE id>0 AND status='failed' AND delivered_at IS NULL),COUNT(DISTINCT slack_user_id),COALESCE(SUM(total_tokens),0),COALESCE(SUM(prompt_tokens),0),COALESCE(SUM(completion_tokens),0),COALESCE(AVG(latency_ms) FILTER(WHERE finished_at IS NOT NULL),0),COALESCE(percentile_cont(0.95) WITHIN GROUP(ORDER BY latency_ms) FILTER(WHERE finished_at IS NOT NULL),0) FROM scoped`, args...).Scan(&out.IssueRequests, &out.ToolIssueRequests, &out.Requests, &out.Queued, &out.Running, &out.Completed, &out.Failed, &out.Undelivered, &out.ActiveUsers, &out.Tokens, &out.PromptTokens, &out.CompletionTokens, &out.AverageMS, &out.P95MS)
	if err != nil {
		return out, err
	}
	rows, err := r.db.Query(ctx, monitorCTE+`SELECT to_char(created_at AT TIME ZONE 'Europe/Istanbul','YYYY-MM-DD'),COUNT(*),SUM(total_tokens),AVG(latency_ms) FROM scoped GROUP BY 1 ORDER BY 1`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var d repository.DailyUsage
		if err = rows.Scan(&d.Date, &d.Messages, &d.Tokens, &d.AverageLatency); err != nil {
			rows.Close()
			return out, err
		}
		out.Daily = append(out.Daily, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// New requests split usage per model, including failed/retried attempts. Old
	// records retain their reported model chain because historical splits are unknown.
	rows, err = r.db.Query(ctx, monitorCTE+`, per_model AS (
 SELECT s.id,u.model,u.provider,u.calls,u.prompt_tokens,u.completion_tokens,u.total_tokens FROM scoped s JOIN nudge_job_model_usage u ON s.id=u.job_id
 UNION ALL SELECT s.id,s.model,COALESCE(a.provider,''),0::bigint,s.prompt_tokens,s.completion_tokens,s.total_tokens FROM scoped s LEFT JOIN nudge_ai_interactions a ON (s.id<0 AND a.id=-s.id) OR (s.id>0 AND a.request_id='slack-job-'||s.id AND a.team_id=s.team_id AND a.slack_user_id=s.slack_user_id)
 WHERE s.model<>'' AND NOT EXISTS(SELECT 1 FROM nudge_job_model_usage u WHERE u.job_id=s.id) AND (s.finished_at IS NOT NULL OR s.total_tokens>0)
 ) SELECT model,provider,COUNT(DISTINCT id),SUM(calls),SUM(prompt_tokens),SUM(completion_tokens),SUM(total_tokens) FROM per_model GROUP BY model,provider ORDER BY SUM(total_tokens) DESC,model LIMIT 100`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var m repository.MonitorModel
		if err = rows.Scan(&m.Model, &m.Provider, &m.Requests, &m.Calls, &m.PromptTokens, &m.CompletionTokens, &m.Tokens); err != nil {
			rows.Close()
			return out, err
		}
		out.Models = append(out.Models, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if teamScope {
		rows, err = r.db.Query(ctx, monitorCTE+`SELECT s.slack_user_id,COALESCE(MAX(u.display_name),''),COUNT(*),SUM(s.total_tokens),COUNT(*) FILTER(WHERE s.status='failed' OR s.tool_failure_count>0),MAX(s.created_at) FROM scoped s LEFT JOIN users u ON u.team_id=s.team_id AND u.slack_user_id=s.slack_user_id GROUP BY s.slack_user_id ORDER BY COUNT(*) DESC,SUM(total_tokens) DESC,slack_user_id LIMIT 50`, args...)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var u repository.MonitorUser
			if err = rows.Scan(&u.User, &u.Name, &u.Requests, &u.Tokens, &u.Errors, &u.LastSeen); err != nil {
				rows.Close()
				return out, err
			}
			out.Users = append(out.Users, u)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	where := "TRUE"
	filtered := append([]any(nil), args...)
	if f.Status == "active" {
		where = "status IN ('queued','running')"
	} else if f.Status == "issues" {
		where = "(status='failed' OR tool_failure_count>0)"
	} else if f.Status == "unanswered" {
		where = "id>0 AND delivered_at IS NULL AND status='failed'"
	} else if f.Status != "" {
		filtered = append(filtered, f.Status)
		where = fmt.Sprintf("status=$%d", len(filtered))
	}
	if f.JobID != 0 {
		filtered = append(filtered, f.JobID)
		where += fmt.Sprintf(" AND id=$%d", len(filtered))
	}
	if err = r.db.QueryRow(ctx, monitorCTE+`SELECT COUNT(*) FROM scoped WHERE `+where, filtered...).Scan(&out.FilteredRequests); err != nil {
		return out, err
	}
	filtered = append(filtered, max(f.Offset, 0))
	rows, err = r.db.Query(ctx, monitorCTE+`SELECT id,slack_user_id,COALESCE((SELECT u.display_name FROM users u WHERE u.team_id=scoped.team_id AND u.slack_user_id=scoped.slack_user_id),''),channel_id,thread_ts,status,stage,model,current_tool,error_code,attempts,trace_count,created_at,updated_at,available_at,lease_until,finished_at,delivered_at,response_ready,COALESCE(status='running' AND lease_until<NOW(),FALSE),latency_ms,prompt_tokens,completion_tokens,total_tokens,trace,tool_failure_count FROM scoped WHERE `+where+` ORDER BY CASE WHEN status IN ('queued','running') THEN 0 ELSE 1 END,created_at DESC,id DESC LIMIT 25 OFFSET $`+fmt.Sprint(len(filtered)), filtered...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var j repository.MonitorRequest
		var raw []byte
		if err = rows.Scan(&j.ID, &j.User, &j.Name, &j.Channel, &j.Thread, &j.Status, &j.Stage, &j.Model, &j.Tool, &j.ErrorCode, &j.Attempts, &j.TraceCount, &j.CreatedAt, &j.UpdatedAt, &j.AvailableAt, &j.LeaseUntil, &j.FinishedAt, &j.DeliveredAt, &j.ResponseReady, &j.Stalled, &j.LatencyMS, &j.PromptTokens, &j.CompletionTokens, &j.TotalTokens, &raw, &j.ToolFailures); err != nil {
			rows.Close()
			return out, err
		}
		if err = json.Unmarshal(raw, &j.Events); err != nil {
			rows.Close()
			return out, err
		}
		out.Recent = append(out.Recent, j)
	}
	err = rows.Err()
	rows.Close()
	out.UpdatedAt = time.Now().UTC()
	return out, err
}
