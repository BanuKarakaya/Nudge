package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"nudge/internal/coding"
	"nudge/internal/scheduler"
	"nudge/internal/workspace"
	"time"
)

func (r *InstallationRepository) ReminderWorkspaces(ctx context.Context) ([]scheduler.WorkspaceSchedule, error) {
	rows, err := r.db.Query(ctx, `SELECT min(u.id),i.team_id,i.timezone FROM slack_installations i JOIN users u ON u.team_id=i.team_id WHERE i.bot_token<>'' AND i.reminders_enabled GROUP BY i.team_id,i.timezone`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []scheduler.WorkspaceSchedule
	for rows.Next() {
		var w scheduler.WorkspaceSchedule
		if err = rows.Scan(&w.User, &w.Team, &w.Timezone); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
func (r *InstallationRepository) ClaimSchedule(ctx context.Context, team, kind, period string) (string, error) {
	id, err := coding.RandomID()
	if err != nil {
		return "", err
	}
	var claimed string
	err = r.db.QueryRow(ctx, `INSERT INTO nudge_schedule_runs(team_id,kind,period,claim,lease_until) VALUES($1,$2,$3,$4,NOW()+INTERVAL '15 minutes') ON CONFLICT(team_id,kind,period) DO UPDATE SET claim=EXCLUDED.claim,lease_until=EXCLUDED.lease_until WHERE NOT nudge_schedule_runs.completed AND nudge_schedule_runs.lease_until<NOW() RETURNING claim`, team, kind, period, id).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return claimed, err
}
func (r *InstallationRepository) FinishSchedule(ctx context.Context, team, kind, period, claim string, success bool) error {
	_, err := r.db.Exec(ctx, `UPDATE nudge_schedule_runs SET completed=$5,claim=NULL,lease_until=NOW()+INTERVAL '5 minutes' WHERE team_id=$1 AND kind=$2 AND period=$3 AND claim=$4`, team, kind, period, claim, success)
	return err
}
func (s *WorkspaceStore) Preferences(ctx context.Context, user int64) (string, bool, error) {
	var zone string
	var enabled bool
	err := s.db.QueryRow(ctx, `SELECT i.timezone,i.reminders_enabled FROM users u JOIN slack_installations i ON i.team_id=u.team_id WHERE u.id=$1`, user).Scan(&zone, &enabled)
	return zone, enabled, err
}
func (s *WorkspaceStore) SavePreferences(ctx context.Context, user int64, zone string, enabled bool) error {
	if _, err := time.LoadLocation(zone); err != nil || len(zone) > 100 {
		return workspace.ErrDenied
	}
	tag, err := s.db.Exec(ctx, `UPDATE slack_installations i SET timezone=$2,reminders_enabled=$3 FROM users u WHERE u.id=$1 AND u.team_id=i.team_id AND i.installer_slack_user_id=u.slack_user_id`, user, zone, enabled)
	if err == nil && tag.RowsAffected() != 1 {
		return workspace.ErrDenied
	}
	return err
}

// Adopt the previous single-workspace setting once, preserving later panel edits.
func (r *InstallationRepository) AdoptLegacyReminder(ctx context.Context, user int64, zone string) error {
	if user <= 0 {
		return nil
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	tag, err := tx.Exec(ctx, `INSERT INTO nudge_schema_migrations(version,checksum) VALUES('legacy-reminder-config','adopted') ON CONFLICT DO NOTHING`)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if _, err = tx.Exec(ctx, `UPDATE slack_installations i SET reminders_enabled=TRUE,timezone=$2 FROM users u WHERE u.id=$1 AND u.team_id=i.team_id`, user, zone); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
