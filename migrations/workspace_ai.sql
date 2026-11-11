CREATE TABLE IF NOT EXISTS workspace_ai_settings (
 team_id TEXT PRIMARY KEY REFERENCES slack_installations(team_id) ON DELETE CASCADE,
 provider TEXT NOT NULL DEFAULT '',
 model TEXT NOT NULL DEFAULT '',
 complex_model TEXT NOT NULL DEFAULT '',
 search_model TEXT NOT NULL DEFAULT '',
 credentials TEXT NOT NULL,
 has_key BOOLEAN NOT NULL DEFAULT FALSE,
 has_search_key BOOLEAN NOT NULL DEFAULT FALSE,
 revision TEXT NOT NULL,
 updated_by BIGINT NOT NULL REFERENCES users(id),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
