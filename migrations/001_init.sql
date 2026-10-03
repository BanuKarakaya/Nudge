-- +goose Up

CREATE TABLE slack_installations (
    team_id TEXT PRIMARY KEY,
    team_name TEXT NOT NULL,
    bot_token TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    team_id TEXT NOT NULL REFERENCES slack_installations(team_id) ON DELETE CASCADE,
    slack_user_id TEXT NOT NULL,
    timezone TEXT NOT NULL DEFAULT 'UTC',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (team_id, slack_user_id)
);

CREATE TABLE raindrop_connections (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    raindrop_user_id BIGINT,
    access_token TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE bookmarks (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    raindrop_bookmark_id BIGINT NOT NULL,
    title TEXT NOT NULL,
    url TEXT NOT NULL,
    summary TEXT,
    saved_at TIMESTAMPTZ NOT NULL,
    is_read BOOLEAN NOT NULL DEFAULT FALSE,
    read_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, raindrop_bookmark_id)
);

CREATE INDEX bookmarks_user_saved_at_idx
    ON bookmarks (user_id, saved_at);

CREATE INDEX bookmarks_unread_idx
    ON bookmarks (user_id, is_read)
    WHERE is_read = FALSE;

CREATE TABLE notifications (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notification_type TEXT NOT NULL,
    period_key TEXT NOT NULL,
    sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, notification_type, period_key)
);

-- +goose Down

DROP TABLE notifications;
DROP TABLE bookmarks;
DROP TABLE raindrop_connections;
DROP TABLE users;
DROP TABLE slack_installations;
