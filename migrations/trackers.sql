CREATE TABLE IF NOT EXISTS tracker_connections (
    provider TEXT NOT NULL CHECK (provider IN ('jira','trello')),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    encrypted TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider,user_id)
);
CREATE TABLE IF NOT EXISTS tracker_oauth_states (
    provider TEXT NOT NULL CHECK (provider IN ('jira','trello')),
    state_hash TEXT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    encrypted TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (provider,state_hash)
);
CREATE INDEX IF NOT EXISTS tracker_oauth_expiry ON tracker_oauth_states(expires_at);
