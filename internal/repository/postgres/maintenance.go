package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func RunMaintenance(ctx context.Context, db *pgxpool.Pool, days int) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		run, cancel := context.WithTimeout(ctx, time.Minute)
		if err := Cleanup(run, db, days); err != nil {
			slog.Error("retention_failed")
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func Cleanup(ctx context.Context, db *pgxpool.Pool, days int) error {
	if days < 1 || days > 3650 {
		return fmt.Errorf("retention must be 1–3650 days")
	}
	for _, q := range []string{
		`DELETE FROM nudge_conversations WHERE updated_at<NOW()-($1*INTERVAL '1 day')`,
		`DELETE FROM nudge_slack_jobs WHERE status IN ('completed','failed') AND finished_at<NOW()-($1*INTERVAL '1 day')`,
		`DELETE FROM nudge_ai_interactions WHERE created_at<NOW()-($1*INTERVAL '1 day')`,
		`DELETE FROM nudge_audit_log WHERE created_at<NOW()-($1*INTERVAL '1 day')`,
		`DELETE FROM nudge_run_budgets WHERE day<CURRENT_DATE-($1::integer)`,
		`DELETE FROM notifications WHERE delivery_status IN ('sent','failed') AND available_at<NOW()-($1*INTERVAL '1 day')`,
		`DELETE FROM coding_edits WHERE status IN ('pushed','cancelled','failed') AND updated_at<NOW()-($1*INTERVAL '1 day')`,
		`DELETE FROM gitlab_edits WHERE status IN ('pushed','cancelled','failed') AND updated_at<NOW()-($1*INTERVAL '1 day')`,
		`DELETE FROM nudge_action_proposals WHERE expires_at<NOW()-($1*INTERVAL '1 day')`,
	} {
		if _, err := db.Exec(ctx, q, days); err != nil {
			return err
		}
	}
	for _, q := range []string{`DELETE FROM nudge_oauth_states WHERE expires_at<NOW()`, `DELETE FROM workspace_sessions WHERE expires_at<NOW()`, `DELETE FROM workspace_used_links WHERE expires_at<NOW()`} {
		if _, err := db.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}
func Metrics(db *pgxpool.Pool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		var jobs, failed, notifications int64
		err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM nudge_slack_jobs WHERE status IN ('queued','running')),(SELECT count(*) FROM nudge_slack_jobs WHERE status='failed'),(SELECT count(*) FROM notifications WHERE delivery_status IN ('pending','sending'))`).Scan(&jobs, &failed, &notifications)
		if err != nil {
			http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		stat := db.Stat()
		fmt.Fprintf(w, "nudgy_jobs_pending %d\nnudgy_jobs_failed %d\nnudgy_notifications_pending %d\nnudgy_db_connections_acquired %d\nnudgy_db_connections_max %d\n", jobs, failed, notifications, stat.AcquiredConns(), stat.MaxConns())
	})
}
