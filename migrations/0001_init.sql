CREATE TABLE IF NOT EXISTS notification_requests (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL DEFAULT 'default',
  idempotency_key TEXT,
  batch_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_notification_requests_tenant_idempotency ON notification_requests(tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS notifications (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL DEFAULT 'default',
  batch_id TEXT NOT NULL,
  recipient TEXT NOT NULL,
  channel TEXT NOT NULL,
  content TEXT NOT NULL,
  priority TEXT NOT NULL,
  status TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 3,
  scheduled_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  correlation_id TEXT,
  idempotency_key TEXT,
  provider_message_id TEXT,
  last_error TEXT
);

CREATE INDEX IF NOT EXISTS idx_notifications_status_channel_created ON notifications(status, channel, created_at);
CREATE INDEX IF NOT EXISTS idx_notifications_due ON notifications(channel, status, scheduled_at, priority, created_at);
CREATE INDEX IF NOT EXISTS idx_notifications_tenant_batch ON notifications(tenant_id, batch_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_tenant_idempotency ON notifications(tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS notification_events (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL DEFAULT 'default',
  notification_id TEXT NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
  batch_id TEXT NOT NULL,
  channel TEXT NOT NULL,
  status TEXT NOT NULL,
  attempt INTEGER NOT NULL DEFAULT 0,
  reason TEXT,
  next_attempt_at TIMESTAMPTZ,
  provider_status INTEGER NOT NULL DEFAULT 0,
  provider_message_id TEXT,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_notification_events_notification ON notification_events(notification_id, id);
CREATE INDEX IF NOT EXISTS idx_notification_events_status_created ON notification_events(status, created_at);
CREATE INDEX IF NOT EXISTS idx_notification_events_tenant_notification ON notification_events(tenant_id, notification_id, id);

CREATE TABLE IF NOT EXISTS rate_limit_windows (
  key TEXT NOT NULL,
  window_start TIMESTAMPTZ NOT NULL,
  limit_count INTEGER NOT NULL,
  used_count INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (key, window_start)
);
