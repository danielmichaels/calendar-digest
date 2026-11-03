CREATE TABLE recipients (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  calendar_id TEXT NOT NULL,
  notify_time TEXT NOT NULL,
  tz TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT 1
);
CREATE TABLE notification_targets (
  id INTEGER PRIMARY KEY,
  recipient_id INTEGER NOT NULL REFERENCES recipients(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK(kind IN ('telegram','email','sms')),
  config TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT 1
);
CREATE INDEX idx_notification_targets_recipient ON notification_targets(recipient_id);
CREATE TABLE digest_snapshots (
  id INTEGER PRIMARY KEY,
  recipient_id INTEGER NOT NULL REFERENCES recipients(id) ON DELETE CASCADE,
  digest_date TEXT NOT NULL,
  token TEXT NOT NULL UNIQUE,
  events TEXT NOT NULL,
  created_at TEXT NOT NULL,
  notified_at TEXT,
  generation INTEGER NOT NULL DEFAULT 1,
  UNIQUE(recipient_id,digest_date)
);
CREATE TABLE app_state (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE outbox (
  id INTEGER PRIMARY KEY,
  dedupe_key TEXT NOT NULL UNIQUE,
  snapshot_id INTEGER REFERENCES digest_snapshots(id) ON DELETE CASCADE,
  target_id INTEGER REFERENCES notification_targets(id) ON DELETE CASCADE,
  payload TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 12,
  available_at INTEGER NOT NULL,
  lease TEXT,
  lease_until INTEGER NOT NULL DEFAULT 0,
  sent_at TEXT,
  last_error TEXT
);
CREATE INDEX idx_outbox_due ON outbox(available_at,lease_until) WHERE sent_at IS NULL;
