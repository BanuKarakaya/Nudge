-- +goose Up

ALTER TABLE raindrop_connections
    ADD COLUMN IF NOT EXISTS refresh_token TEXT,
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;

-- +goose Down

ALTER TABLE raindrop_connections
    DROP COLUMN expires_at,
    DROP COLUMN refresh_token;
