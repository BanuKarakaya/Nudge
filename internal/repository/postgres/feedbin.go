package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"nudge/internal/domain"
	"nudge/internal/repository"
)

var _ repository.FeedbinConnectionRepository = (*FeedbinConnectionRepository)(nil)

type FeedbinConnectionRepository struct {
	db *pgxpool.Pool
}

func NewFeedbinConnectionRepository(db *pgxpool.Pool) *FeedbinConnectionRepository {
	return &FeedbinConnectionRepository{db: db}
}

func (r *FeedbinConnectionRepository) Save(ctx context.Context, connection domain.FeedbinConnection) error {
	const query = `
		INSERT INTO feedbin_connections (user_id, email, password)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET
			email = EXCLUDED.email,
			password = EXCLUDED.password,
			updated_at = NOW()`

	_, err := r.db.Exec(ctx, query,
		connection.UserID,
		connection.Email,
		connection.Password,
	)
	return err
}

func (r *FeedbinConnectionRepository) GetByUserID(ctx context.Context, userID int64) (domain.FeedbinConnection, error) {
	const query = `
		SELECT user_id, email, password, created_at, updated_at
		FROM feedbin_connections
		WHERE user_id = $1`

	var connection domain.FeedbinConnection
	err := r.db.QueryRow(ctx, query, userID).Scan(
		&connection.UserID,
		&connection.Email,
		&connection.Password,
		&connection.CreatedAt,
		&connection.UpdatedAt,
	)
	return connection, err
}
