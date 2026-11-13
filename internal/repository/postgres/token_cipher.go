package postgres

import (
	"context"
	"nudge/internal/secure"
	"strconv"
	"strings"
)

func encryptToken(c secure.Cipher, value, binding string) (string, error) {
	if value == "" || !c.Configured() {
		return value, nil
	}
	return c.EncryptFor(value, binding)
}
func decryptToken(c secure.Cipher, value, binding string) (string, error) {
	if value == "" || !strings.HasPrefix(value, "v1:") && !strings.HasPrefix(value, "v2:") {
		return value, nil
	}
	return c.DecryptFor(value, binding)
}

// MigrateTokens replaces legacy plaintext and rotates prior key IDs without
// overwriting a concurrent reconnect. Values never leave this process.
func (r *InstallationRepository) MigrateTokens(ctx context.Context) error {
	rows, err := r.db.Query(ctx, `SELECT team_id,bot_token FROM slack_installations WHERE bot_token<>''`)
	if err != nil {
		return err
	}
	type tokenRow struct{ team, value string }
	var pending []tokenRow
	for rows.Next() {
		var row tokenRow
		if err = rows.Scan(&row.team, &row.value); err != nil {
			rows.Close()
			return err
		}
		if r.cipher.NeedsRotation(row.value) {
			pending = append(pending, row)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, row := range pending {
		plain, err := decryptToken(r.cipher, row.value, "slack:"+row.team)
		if err != nil {
			return err
		}
		encrypted, err := encryptToken(r.cipher, plain, "slack:"+row.team)
		if err != nil {
			return err
		}
		if _, err = r.db.Exec(ctx, `UPDATE slack_installations SET bot_token=$3 WHERE team_id=$1 AND bot_token=$2`, row.team, row.value, encrypted); err != nil {
			return err
		}
	}
	return nil
}
func (r *RaindropConnectionRepository) MigrateTokens(ctx context.Context) error {
	rows, err := r.db.Query(ctx, `SELECT user_id,access_token,COALESCE(refresh_token,'') FROM raindrop_connections`)
	if err != nil {
		return err
	}
	type tokenRow struct {
		user            int64
		access, refresh string
	}
	var pending []tokenRow
	for rows.Next() {
		var row tokenRow
		if err = rows.Scan(&row.user, &row.access, &row.refresh); err != nil {
			rows.Close()
			return err
		}
		if r.cipher.NeedsRotation(row.access) || row.refresh != "" && r.cipher.NeedsRotation(row.refresh) {
			pending = append(pending, row)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, row := range pending {
		binding := strconv.FormatInt(row.user, 10)
		access, err := decryptToken(r.cipher, row.access, "raindrop-access:"+binding)
		if err != nil {
			return err
		}
		refresh, err := decryptToken(r.cipher, row.refresh, "raindrop-refresh:"+binding)
		if err != nil {
			return err
		}
		access, err = encryptToken(r.cipher, access, "raindrop-access:"+binding)
		if err != nil {
			return err
		}
		refresh, err = encryptToken(r.cipher, refresh, "raindrop-refresh:"+binding)
		if err != nil {
			return err
		}
		if _, err = r.db.Exec(ctx, `UPDATE raindrop_connections SET access_token=$4,refresh_token=$5 WHERE user_id=$1 AND access_token=$2 AND COALESCE(refresh_token,'')=$3`, row.user, row.access, row.refresh, access, refresh); err != nil {
			return err
		}
	}
	return nil
}
