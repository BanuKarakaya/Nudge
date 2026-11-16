package postgres

import "context"

func (r *InstallationRepository) ClaimProfileRefresh(ctx context.Context, user int64) (bool, error) {
	tag, err := r.db.Exec(ctx, `UPDATE users SET profile_checked_at=NOW() WHERE id=$1 AND (profile_checked_at IS NULL OR profile_checked_at<NOW()-INTERVAL '24 hours')`, user)
	return tag.RowsAffected() == 1, err
}
func (r *InstallationRepository) SaveProfileName(ctx context.Context, user int64, name string) error {
	if len([]rune(name)) > 100 {
		return nil
	}
	_, err := r.db.Exec(ctx, `UPDATE users SET display_name=$2 WHERE id=$1`, user, name)
	return err
}
