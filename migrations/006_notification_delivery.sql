-- Preserve existing notifications as sent. New rows are explicitly pending.
ALTER TABLE notifications
 ADD COLUMN IF NOT EXISTS delivery_status TEXT NOT NULL DEFAULT 'sent',
 ADD COLUMN IF NOT EXISTS payload JSONB,
 ADD COLUMN IF NOT EXISTS available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 ADD COLUMN IF NOT EXISTS lease_until TIMESTAMPTZ,
 ADD COLUMN IF NOT EXISTS claim_token TEXT,
 ADD COLUMN IF NOT EXISTS attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE notifications ALTER COLUMN sent_at DROP NOT NULL;
ALTER TABLE notifications ALTER COLUMN sent_at DROP DEFAULT;
CREATE INDEX IF NOT EXISTS notifications_delivery_due_idx
 ON notifications (available_at) WHERE delivery_status IN ('pending', 'sending');
