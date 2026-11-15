CREATE TABLE workspace_mcp_connections (
    team_id TEXT NOT NULL REFERENCES slack_installations(team_id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    revision TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    tools JSONB NOT NULL DEFAULT '[]'::jsonb,
    credentials TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (team_id, id)
);
