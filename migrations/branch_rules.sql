CREATE TABLE IF NOT EXISTS coding_branch_rules (
 team_id TEXT NOT NULL REFERENCES slack_installations(team_id) ON DELETE CASCADE,
 slack_user_id TEXT NOT NULL DEFAULT '',
 template TEXT NOT NULL CHECK (length(template) BETWEEN 1 AND 160),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(team_id, slack_user_id)
);
