-- Historical installations deliberately remain unclaimed until Slack reinstall.
-- Never infer ownership from an arbitrary existing member or personal token.
ALTER TABLE slack_installations ADD COLUMN IF NOT EXISTS installer_slack_user_id TEXT;
CREATE TABLE IF NOT EXISTS workspace_integrations (
 team_id TEXT NOT NULL REFERENCES slack_installations(team_id) ON DELETE CASCADE,
 provider TEXT NOT NULL CHECK (provider IN ('github','gitlab','jira','trello')),
 enabled BOOLEAN NOT NULL DEFAULT FALSE,
 resources JSONB NOT NULL DEFAULT '[]',
 updated_by BIGINT NOT NULL REFERENCES users(id),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(team_id,provider)
);
CREATE TABLE IF NOT EXISTS workspace_sessions (
 token_hash TEXT PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS workspace_used_links (
 token_hash TEXT PRIMARY KEY,
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS workspace_sessions_expiry_idx ON workspace_sessions(expires_at);
CREATE INDEX IF NOT EXISTS workspace_used_links_expiry_idx ON workspace_used_links(expires_at);

ALTER TABLE slack_installations ADD COLUMN IF NOT EXISTS installed_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
