package postgres

import (
	"context"
	"encoding/json"
)

func (s *WorkspaceStore) ExportPersonalData(ctx context.Context, user int64) (json.RawMessage, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT jsonb_build_object(
 'profile',jsonb_build_object('user_id',u.id,'team',u.team_id,'slack_user',u.slack_user_id,'timezone',u.timezone),
 'bookmarks',COALESCE((SELECT jsonb_agg(to_jsonb(b)) FROM bookmarks b WHERE b.user_id=u.id),'[]'),
 'conversations',COALESCE((SELECT jsonb_agg(jsonb_build_object('channel',c.channel_id,'thread',c.thread_ts,'state',c.state)) FROM nudge_conversations c WHERE c.team_id=u.team_id AND c.slack_user_id=u.slack_user_id),'[]'),
 'activity',COALESCE((SELECT jsonb_agg(to_jsonb(a)) FROM nudge_ai_interactions a WHERE a.user_id=u.id AND a.team_id=u.team_id),'[]')) FROM users u WHERE u.id=$1`, user).Scan(&raw)
	return raw, err
}
func (s *WorkspaceStore) DeletePersonalData(ctx context.Context, user int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	for _, table := range []string{"bookmarks", "notifications", "feedbin_connections", "raindrop_connections", "coding_connections", "coding_oauth_states", "coding_tasks", "coding_edits", "gitlab_connections", "gitlab_oauth_states", "gitlab_tasks", "gitlab_edits", "tracker_connections", "tracker_oauth_states", "nudge_action_proposals", "nudge_merge_intents", "workspace_sessions"} {
		if _, err = tx.Exec(ctx, `DELETE FROM `+table+` WHERE user_id=$1`, user); err != nil {
			return err
		}
	}
	for _, query := range []string{
		`DELETE FROM nudge_slack_jobs j USING users u WHERE u.id=$1 AND j.team_id=u.team_id AND j.slack_user_id=u.slack_user_id`,
		`DELETE FROM nudge_conversations c USING users u WHERE u.id=$1 AND c.team_id=u.team_id AND c.slack_user_id=u.slack_user_id`,
		`DELETE FROM nudge_ai_interactions WHERE user_id=$1`,
		`INSERT INTO nudge_audit_log(team_id,actor_id,action,target) SELECT team_id,id,'personal_data.delete','personal history and credentials' FROM users WHERE id=$1`,
	} {
		if _, err = tx.Exec(ctx, query, user); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *WorkspaceStore) AuditLog(ctx context.Context, user int64) (json.RawMessage, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(row),'[]') FROM (SELECT a.id,a.actor_id,a.action,a.target,a.created_at FROM nudge_audit_log a JOIN users u ON u.team_id=a.team_id JOIN slack_installations i ON i.team_id=u.team_id WHERE u.id=$1 AND i.installer_slack_user_id=u.slack_user_id ORDER BY a.id DESC LIMIT 200) row`, user).Scan(&raw)
	return raw, err
}
