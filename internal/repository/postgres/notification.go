package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"nudge/internal/repository"
	"nudge/migrations"
)

var _ repository.NotificationRepository = (*NotificationRepository)(nil)

type NotificationRepository struct{ db *pgxpool.Pool }

func NewNotificationRepository(ctx context.Context, db *pgxpool.Pool) (*NotificationRepository, error) {
	if _, err := db.Exec(ctx, migrations.NotificationSchema); err != nil {
		return nil, err
	}
	return &NotificationRepository{db: db}, nil
}
func (r *NotificationRepository) Exists(ctx context.Context, userID int64, kind, period string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notifications WHERE user_id=$1 AND notification_type=$2 AND period_key=$3)`, userID, kind, period).Scan(&exists)
	return exists, err
}
func (r *NotificationRepository) Enqueue(ctx context.Context, userID int64, kind, period string, message repository.NotificationMessage) error {
	if userID <= 0 || kind == "" || period == "" || message.Channel == "" || (message.Text == "" && message.Bookmark == nil) {
		return errors.New("invalid notification")
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `INSERT INTO notifications (user_id, notification_type, period_key, delivery_status, payload, sent_at)
 VALUES ($1,$2,$3,'pending',$4,NULL) ON CONFLICT (user_id, notification_type, period_key) DO NOTHING`, userID, kind, period, payload)
	return err
}
func (r *NotificationRepository) ClaimNext(ctx context.Context) (repository.NotificationDelivery, error) {
	var d repository.NotificationDelivery
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return d, err
	}
	token := hex.EncodeToString(key[:])
	var payload []byte
	// Lease tokens prevent a stale worker from updating another worker's claim.
	err := r.db.QueryRow(ctx, `WITH candidate AS (
 SELECT id FROM notifications WHERE
 (delivery_status='pending' AND available_at <= NOW()) OR
 (delivery_status='sending' AND lease_until <= NOW())
 ORDER BY available_at, id FOR UPDATE SKIP LOCKED LIMIT 1
 ) UPDATE notifications n SET delivery_status='sending', lease_until=NOW()+INTERVAL '2 minutes',
 claim_token=$1, attempts=n.attempts+1 FROM candidate c WHERE n.id=c.id
 RETURNING n.id,n.user_id,n.claim_token,n.attempts,n.payload`, token).Scan(&d.ID, &d.UserID, &d.ClaimToken, &d.Attempts, &payload)
	if err != nil {
		return d, err
	}
	err = json.Unmarshal(payload, &d.Message)
	return d, err
}
func (r *NotificationRepository) Complete(ctx context.Context, d repository.NotificationDelivery) error {
	result, err := r.db.Exec(ctx, `UPDATE notifications SET delivery_status='sent', sent_at=NOW(), lease_until=NULL, claim_token=NULL
 WHERE id=$1 AND claim_token=$2 AND delivery_status='sending'`, d.ID, d.ClaimToken)
	if err == nil && result.RowsAffected() != 1 {
		return errors.New("notification lease lost")
	}
	return err
}
func (r *NotificationRepository) Retry(ctx context.Context, d repository.NotificationDelivery, delay time.Duration) error {
	result, err := r.db.Exec(ctx, `UPDATE notifications SET delivery_status='pending', available_at=NOW()+($3 * INTERVAL '1 second'), lease_until=NULL, claim_token=NULL
 WHERE id=$1 AND claim_token=$2 AND delivery_status='sending'`, d.ID, d.ClaimToken, delay.Seconds())
	if err == nil && result.RowsAffected() != 1 {
		return errors.New("notification lease lost")
	}
	return err
}
