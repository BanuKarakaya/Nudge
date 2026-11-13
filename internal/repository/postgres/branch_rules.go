package postgres

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/coding"
	"nudge/migrations"
)

type BranchRuleStore struct{ db *pgxpool.Pool }

func NewBranchRuleStore(ctx context.Context, db *pgxpool.Pool) (*BranchRuleStore, error) {
	if err := migrations.Apply(ctx, db); err != nil {
		return nil, err
	}
	return &BranchRuleStore{db: db}, nil
}
func (s *BranchRuleStore) BranchTemplate(ctx context.Context, i coding.Identity) (string, error) {
	var template string
	// Identity is checked against users even when no custom rule exists.
	err := s.db.QueryRow(ctx, `SELECT COALESCE(
 (SELECT template FROM coding_branch_rules WHERE team_id=u.team_id AND slack_user_id=''),
 (SELECT template FROM coding_branch_rules WHERE team_id=u.team_id AND slack_user_id=u.slack_user_id),$3)
 FROM users u WHERE u.id=$1 AND u.team_id=$2`, i.UserID, i.Team, coding.DefaultBranchTemplate).Scan(&template)
	return template, err
}
func (s *BranchRuleStore) Settings(ctx context.Context) (coding.BranchSettings, error) {
	result := coding.BranchSettings{Rules: []coding.BranchRule{}, Scopes: []coding.BranchScope{}}
	rows, err := s.db.Query(ctx, `SELECT i.team_id,i.team_name,COALESCE(u.slack_user_id,'') FROM slack_installations i LEFT JOIN users u ON u.team_id=i.team_id ORDER BY i.team_name,u.slack_user_id`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var v coding.BranchScope
		if err = rows.Scan(&v.Team, &v.Name, &v.User); err != nil {
			rows.Close()
			return result, err
		}
		result.Scopes = append(result.Scopes, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	rows, err = s.db.Query(ctx, `SELECT team_id,slack_user_id,template FROM coding_branch_rules ORDER BY team_id,slack_user_id`)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var v coding.BranchRule
		if err = rows.Scan(&v.Team, &v.User, &v.Template); err != nil {
			return result, err
		}
		result.Rules = append(result.Rules, v)
	}
	return result, rows.Err()
}
func (s *BranchRuleStore) SaveRule(ctx context.Context, r coding.BranchRule) error {
	if r.Team == "" || len(r.Team) > 100 || len(r.User) > 100 {
		return coding.ErrArguments
	}
	if r.Template != "" {
		if _, err := coding.ValidateBranchTemplate(r.Template); err != nil {
			return err
		}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM slack_installations i WHERE i.team_id=$1 AND ($2='' OR EXISTS(SELECT 1 FROM users u WHERE u.team_id=i.team_id AND u.slack_user_id=$2)))`, r.Team, r.User).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return coding.ErrDenied
	}
	if r.Template == "" {
		_, err = tx.Exec(ctx, `DELETE FROM coding_branch_rules WHERE team_id=$1 AND slack_user_id=$2`, r.Team, r.User)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO coding_branch_rules(team_id,slack_user_id,template) VALUES($1,$2,$3) ON CONFLICT(team_id,slack_user_id) DO UPDATE SET template=EXCLUDED.template,updated_at=NOW()`, r.Team, r.User, r.Template)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var _ coding.BranchSettingsStore = (*BranchRuleStore)(nil)
