package postgres

import (
	"context"
	"nudge/internal/repository"
)

func (r *AnalyticsRepository) RecordHTTPError(ctx context.Context, user int64, e repository.HTTPError) error {
	_, err := r.db.Exec(ctx, `INSERT INTO nudge_http_errors(user_id,request_id,method,route,status,duration_ms) VALUES($1,$2,$3,$4,$5,$6)`, user, e.RequestID, e.Method, e.Route, e.Status, e.DurationMS)
	return err
}
func (r *AnalyticsRepository) monitorHTTPErrors(ctx context.Context, user int64, team bool, f repository.MonitorFilters) ([]repository.HTTPError, error) {
	rows, err := r.db.Query(ctx, `SELECT e.request_id,e.method,e.route,e.status,e.duration_ms,e.created_at FROM nudge_http_errors e JOIN users u ON u.id=e.user_id JOIN users viewer ON viewer.id=$1 WHERE u.team_id=viewer.team_id AND ($2 OR e.user_id=$1) AND ($3='' OR u.slack_user_id=$3) AND e.created_at>=$4 AND e.created_at<$5 ORDER BY e.created_at DESC LIMIT 100`, user, team, f.User, f.From, f.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []repository.HTTPError{}
	for rows.Next() {
		var e repository.HTTPError
		if err = rows.Scan(&e.RequestID, &e.Method, &e.Route, &e.Status, &e.DurationMS, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
