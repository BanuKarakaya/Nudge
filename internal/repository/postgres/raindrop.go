package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"nudge/internal/domain"
	"nudge/internal/repository"
)

var _ repository.RaindropConnectionRepository = (*RaindropConnectionRepository)(nil)

type RaindropConnectionRepository struct {
	db *pgxpool.Pool
}

func NewRaindropConnectionRepository(db *pgxpool.Pool) *RaindropConnectionRepository {
	return &RaindropConnectionRepository{db: db}
}

func (r *RaindropConnectionRepository) Save(ctx context.Context, connection domain.RaindropConnection) error {
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

	_, err := r.db.Exec(ctx, query,
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
		SELECT user_id, raindrop_user_id, access_token, refresh_token, expires_at
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
	return connection, err
}
