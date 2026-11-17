package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"nudge/internal/analytics"
	"nudge/internal/repository"
)

func traceBinding(job int64) string { return fmt.Sprintf("model-trace:%d", job) }
func (r *SlackJobRepository) ReportModelCall(ctx context.Context, job repository.SlackJob, call analytics.ModelCall) error {
	contents := ""
	if r.traceCipher.Configured() {
		raw, err := json.Marshal(map[string]string{"request": call.Request, "response": call.Response})
		if err != nil {
			return err
		}
		if len(raw) <= 512<<10 {
			contents, err = r.traceCipher.EncryptFor(string(raw), traceBinding(job.ID))
			if err != nil {
				return err
			}
		}
	}
	call.Request, call.Response = "", ""
	raw, err := json.Marshal(call)
	if err != nil {
		return err
	}

	// Count every HTTP attempt, even failed requests without usage or captures
	// beyond the bounded content window. Keep ownership and capture atomic.
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = jobUpdated(tx.Exec(ctx, `UPDATE nudge_slack_jobs SET model_call_count=model_call_count+1 WHERE `+ownedSlackJob, job.ID, job.ClaimToken)); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO nudge_model_calls(job_id,attempt,metadata,contents)
 SELECT $1,$2,$3,$4 WHERE (SELECT COUNT(*) FROM nudge_model_calls WHERE job_id=$1)<128`, job.ID, job.Attempts, raw, contents)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *AnalyticsRepository) monitorContents(ctx context.Context, team, owner string, j *repository.MonitorRequest) error {
	if j.ID <= 0 {
		return nil
	}
	// Content is never granted by manager role. A workspace summary still exposes
	// safe call metadata, but only the requester can read conversation/model text.
	j.ContentVisible = j.User == owner
	if j.ContentVisible {
		err := r.db.QueryRow(ctx, `SELECT COALESCE(payload#>>'{event,text}',''),COALESCE(result->>'Message','') FROM nudge_slack_jobs WHERE id=$1 AND team_id=$2 AND slack_user_id=$3`, j.ID, team, owner).Scan(&j.RequestText, &j.ResponseText)
		if err != nil {
			return err
		}
		j.RequestText = analytics.RedactText(j.RequestText)
		j.ResponseText = analytics.RedactText(j.ResponseText)
	}
	rows, err := r.db.Query(ctx, `SELECT metadata,CASE WHEN $3 THEN contents ELSE '' END FROM nudge_model_calls c JOIN nudge_slack_jobs j ON j.id=c.job_id WHERE c.job_id=$1 AND j.team_id=$2 ORDER BY c.id LIMIT 128`, j.ID, team, j.ContentVisible)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var encrypted string
		if err = rows.Scan(&raw, &encrypted); err != nil {
			return err
		}
		var call analytics.ModelCall
		if err = json.Unmarshal(raw, &call); err != nil {
			return err
		}
		if encrypted != "" && r.traceCipher.Configured() {
			plain, decryptErr := r.traceCipher.DecryptFor(encrypted, traceBinding(j.ID))
			if decryptErr == nil {
				var content map[string]string
				if json.Unmarshal([]byte(plain), &content) == nil {
					call.Request = content["request"]
					call.Response = content["response"]
				}
			}
		}
		j.ModelCalls = append(j.ModelCalls, call)
	}
	return rows.Err()
}
