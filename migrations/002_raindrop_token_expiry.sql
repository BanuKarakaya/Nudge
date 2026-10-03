-- +goose Up

ALTER TABLE raindrop_connections
    ADD COLUMN refresh_token TEXT,
    ADD COLUMN expires_at TIMESTAMPTZ;

-- +goose Down

ALTER TABLE raindrop_connections
    DROP COLUMN expires_at,
    DROP COLUMN refresh_token;
