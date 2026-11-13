package postgres

import (
	"context"
	"nudge/internal/secure"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"nudge/internal/domain"
	"nudge/internal/repository"
)

var _ repository.RaindropConnectionRepository = (*RaindropConnectionRepository)(nil)

type RaindropConnectionRepository struct {
	db     *pgxpool.Pool
	cipher secure.Cipher
}

func NewRaindropConnectionRepository(db *pgxpool.Pool, ciphers ...secure.Cipher) *RaindropConnectionRepository {
	r := &RaindropConnectionRepository{db: db}
	if len(ciphers) > 0 {
		r.cipher = ciphers[0]
	}
	return r
}

func (r *RaindropConnectionRepository) Save(ctx context.Context, connection domain.RaindropConnection) error {
	var err error
	connection.AccessToken, err = encryptToken(r.cipher, connection.AccessToken, "raindrop-access:"+strconv.FormatInt(connection.UserID, 10))
	if err != nil {
		return err
	}
	connection.RefreshToken, err = encryptToken(r.cipher, connection.RefreshToken, "raindrop-refresh:"+strconv.FormatInt(connection.UserID, 10))
	if err != nil {
		return err
	}
	const query = `
		INSERT INTO raindrop_connections (
			user_id, raindrop_user_id, access_token, refresh_token, expires_at
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id) DO UPDATE SET
			raindrop_user_id = EXCLUDED.raindrop_user_id,
			access_token = EXCLUDED.access_token,
			refresh_token = EXCLUDED.refresh_token,
			expires_at = EXCLUDED.expires_at,
			updated_at = NOW()`

	_, err = r.db.Exec(ctx, query,
		connection.UserID,
		connection.RaindropUserID,
		connection.AccessToken,
		connection.RefreshToken,
		connection.ExpiresAt,
	)
	return err
}

func (r *RaindropConnectionRepository) GetByUserID(ctx context.Context, userID int64) (domain.RaindropConnection, error) {
	const query = `
		SELECT user_id, raindrop_user_id, access_token, COALESCE(refresh_token,''), expires_at
		FROM raindrop_connections
		WHERE user_id = $1`

	var connection domain.RaindropConnection
	err := r.db.QueryRow(ctx, query, userID).Scan(
		&connection.UserID,
		&connection.RaindropUserID,
		&connection.AccessToken,
		&connection.RefreshToken,
		&connection.ExpiresAt,
	)
	if err == nil {
		connection.AccessToken, err = decryptToken(r.cipher, connection.AccessToken, "raindrop-access:"+strconv.FormatInt(userID, 10))
	}
	if err == nil {
		connection.RefreshToken, err = decryptToken(r.cipher, connection.RefreshToken, "raindrop-refresh:"+strconv.FormatInt(userID, 10))
	}
	return connection, err
}

// RefreshConnection serializes refresh-token rotation across workers/replicas.
// fn must only perform the bounded token refresh, never subsequent list calls.
func (r *RaindropConnectionRepository) RefreshConnection(ctx context.Context, user int64, fn func(*domain.RaindropConnection) error) (domain.RaindropConnection, error) {
	var c domain.RaindropConnection
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer tx.Rollback(context.Background())
	err = tx.QueryRow(ctx, `SELECT user_id,raindrop_user_id,access_token,COALESCE(refresh_token,''),expires_at FROM raindrop_connections WHERE user_id=$1 FOR UPDATE`, user).Scan(&c.UserID, &c.RaindropUserID, &c.AccessToken, &c.RefreshToken, &c.ExpiresAt)
	if err != nil {
		return c, err
	}
	c.AccessToken, err = decryptToken(r.cipher, c.AccessToken, "raindrop-access:"+strconv.FormatInt(user, 10))
	if err != nil {
		return c, err
	}
	c.RefreshToken, err = decryptToken(r.cipher, c.RefreshToken, "raindrop-refresh:"+strconv.FormatInt(user, 10))
	if err != nil {
		return c, err
	}
	if err = fn(&c); err != nil {
		return c, err
	}
	access, err := encryptToken(r.cipher, c.AccessToken, "raindrop-access:"+strconv.FormatInt(user, 10))
	if err != nil {
		return c, err
	}
	refresh, err := encryptToken(r.cipher, c.RefreshToken, "raindrop-refresh:"+strconv.FormatInt(user, 10))
	if err != nil {
		return c, err
	}
	_, err = tx.Exec(ctx, `UPDATE raindrop_connections SET access_token=$2,refresh_token=$3,expires_at=$4,updated_at=NOW() WHERE user_id=$1`, user, access, refresh, c.ExpiresAt)
	if err != nil {
		return c, err
	}
	return c, tx.Commit(ctx)
}
