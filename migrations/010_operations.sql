CREATE TABLE IF NOT EXISTS nudge_oauth_states (
 state_hash TEXT PRIMARY KEY,
 purpose TEXT NOT NULL,
 user_id BIGINT,
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS nudge_oauth_states_expiry ON nudge_oauth_states(expires_at);
CREATE TABLE IF NOT EXISTS nudge_audit_log (
 id BIGSERIAL PRIMARY KEY,
 team_id TEXT NOT NULL,
 actor_id BIGINT,
 action TEXT NOT NULL,
 target TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS nudge_audit_team_time ON nudge_audit_log(team_id,created_at DESC);
CREATE TABLE IF NOT EXISTS nudge_action_policies (
 team_id TEXT NOT NULL REFERENCES slack_installations(team_id) ON DELETE CASCADE,
 tool TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('auto','approval','disabled')),
 updated_by BIGINT NOT NULL REFERENCES users(id),
 PRIMARY KEY(team_id,tool)
);
CREATE TABLE IF NOT EXISTS nudge_action_proposals (
 id TEXT PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 team_id TEXT NOT NULL,
 channel_id TEXT NOT NULL,
 thread_ts TEXT NOT NULL,
 tool TEXT NOT NULL,
 payload TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','executing','done','failed')),
 expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW()+INTERVAL '20 minutes',
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE OR REPLACE FUNCTION nudge_audit_configuration() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO nudge_audit_log(team_id,actor_id,action,target)
 VALUES(NEW.team_id,NEW.updated_by,TG_TABLE_NAME || '.update',COALESCE(to_jsonb(NEW)->>'provider',to_jsonb(NEW)->>'tool','ai'));
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS nudge_audit_configuration ON workspace_integrations;
CREATE TRIGGER nudge_audit_configuration AFTER INSERT OR UPDATE ON workspace_integrations FOR EACH ROW EXECUTE FUNCTION nudge_audit_configuration();
DROP TRIGGER IF EXISTS nudge_audit_configuration ON workspace_ai_settings;
CREATE TRIGGER nudge_audit_configuration AFTER INSERT OR UPDATE ON workspace_ai_settings FOR EACH ROW EXECUTE FUNCTION nudge_audit_configuration();
DROP TRIGGER IF EXISTS nudge_audit_configuration ON nudge_action_policies;
CREATE TRIGGER nudge_audit_configuration AFTER INSERT OR UPDATE ON nudge_action_policies FOR EACH ROW EXECUTE FUNCTION nudge_audit_configuration();
CREATE TABLE IF NOT EXISTS nudge_run_budgets (
 id TEXT PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 team_id TEXT NOT NULL,
 day DATE NOT NULL DEFAULT (NOW() AT TIME ZONE 'UTC')::date,
 tokens BIGINT NOT NULL,
 running BOOLEAN NOT NULL DEFAULT TRUE,
 lease_until TIMESTAMPTZ NOT NULL DEFAULT NOW()+INTERVAL '10 minutes'
);
CREATE INDEX IF NOT EXISTS nudge_run_budgets_day ON nudge_run_budgets(team_id,day);
ALTER TABLE slack_installations ADD COLUMN IF NOT EXISTS timezone TEXT NOT NULL DEFAULT 'UTC';
ALTER TABLE slack_installations ADD COLUMN IF NOT EXISTS reminders_enabled BOOLEAN NOT NULL DEFAULT FALSE;
CREATE TABLE IF NOT EXISTS nudge_schedule_runs (
 team_id TEXT NOT NULL REFERENCES slack_installations(team_id) ON DELETE CASCADE,
 kind TEXT NOT NULL,
 period TEXT NOT NULL,
 claim TEXT,
 lease_until TIMESTAMPTZ,
 completed BOOLEAN NOT NULL DEFAULT FALSE,
 PRIMARY KEY(team_id,kind,period)
);

ALTER TABLE workspace_ai_settings ADD COLUMN IF NOT EXISTS tool_models JSONB NOT NULL DEFAULT '{}';
