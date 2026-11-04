CREATE TABLE IF NOT EXISTS gitlab_connections (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    gitlab_user_id BIGINT NOT NULL,
    login TEXT NOT NULL,
    credentials TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS gitlab_oauth_states (
    state_hash TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    verifier TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
ALTER TABLE gitlab_oauth_states ADD COLUMN IF NOT EXISTS phase TEXT NOT NULL DEFAULT 'legacy';
ALTER TABLE gitlab_oauth_states ADD COLUMN IF NOT EXISTS installation_id BIGINT NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS gitlab_tasks (
    id TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id TEXT NOT NULL,
    channel_id TEXT NOT NULL,
    thread_ts TEXT NOT NULL,
    event_id TEXT NOT NULL,
    provider TEXT NOT NULL CHECK (provider = 'gitlab'),
    repository JSONB NOT NULL,
    instruction TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('draft', 'cancelled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, team_id, channel_id, event_id)
);
CREATE INDEX IF NOT EXISTS gitlab_tasks_owner_idx ON gitlab_tasks(user_id, team_id, created_at DESC);

CREATE TABLE IF NOT EXISTS gitlab_edits (
    id TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id TEXT NOT NULL,
    channel_id TEXT NOT NULL,
    thread_ts TEXT NOT NULL,
    event_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('cloning','editing','awaiting_user_test','pushed','cancelled')),
    payload TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, team_id, channel_id, event_id)
);
CREATE INDEX IF NOT EXISTS gitlab_edits_owner_idx ON gitlab_edits(user_id, team_id, created_at DESC);
