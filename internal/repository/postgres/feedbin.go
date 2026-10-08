package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"nudge/internal/domain"
	"nudge/internal/repository"
	"nudge/internal/secure"
)

var _ repository.FeedbinConnectionRepository = (*FeedbinConnectionRepository)(nil)

type FeedbinConnectionRepository struct {
	db     *pgxpool.Pool
	cipher secure.Cipher
}

func NewFeedbinConnectionRepository(db *pgxpool.Pool, passwordCipher secure.Cipher) *FeedbinConnectionRepository {
	return &FeedbinConnectionRepository{db: db, cipher: passwordCipher}
}

func (r *FeedbinConnectionRepository) Save(ctx context.Context, connection domain.FeedbinConnection) error {
	encryptedPassword, err := r.cipher.Encrypt(connection.Password)
	if err != nil {
		return err
	}

	const query = `
		INSERT INTO feedbin_connections (user_id, email, password)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET
			email = EXCLUDED.email,
			password = EXCLUDED.password,
			updated_at = NOW()`

	_, err = r.db.Exec(ctx, query,
		connection.UserID,
		connection.Email,
		encryptedPassword,
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
	if err != nil {
		return connection, err
	}
	connection.Password, err = r.cipher.Decrypt(connection.Password)
	return connection, err
}
