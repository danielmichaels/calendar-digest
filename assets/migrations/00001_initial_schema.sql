-- +goose Up
-- +goose StatementBegin
CREATE TABLE recipients
(
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL,
    calendar_id TEXT    NOT NULL,
    -- Local to tz, "HH:MM". Stored as text so the value read back is the value
    -- entered: an integer minutes-past-midnight would need every reader to
    -- agree on the same encoding.
    notify_time TEXT    NOT NULL,
    tz          TEXT    NOT NULL,
    enabled     BOOLEAN NOT NULL DEFAULT 1
);

-- 'sms' is present from the start deliberately. Altering a CHECK in SQLite
-- needs a full table rebuild, which is free now and disruptive once the table
-- holds live rows.
CREATE TABLE notification_targets
(
    id           INTEGER PRIMARY KEY,
    recipient_id INTEGER NOT NULL REFERENCES recipients (id) ON DELETE CASCADE,
    kind         TEXT    NOT NULL CHECK (kind IN ('telegram', 'email', 'sms')),
    -- JSON, shaped by kind: {"chat_id"} / {"address"} / {"phone"}.
    config       TEXT    NOT NULL,
    enabled      BOOLEAN NOT NULL DEFAULT 1
);

-- SQLite does not index a foreign key automatically, so without this both the
-- nightly per-recipient fan-out and the ON DELETE CASCADE scan the table.
CREATE INDEX idx_notification_targets_recipient ON notification_targets (recipient_id);

CREATE TABLE digest_snapshots
(
    id           INTEGER PRIMARY KEY,
    recipient_id INTEGER NOT NULL REFERENCES recipients (id) ON DELETE CASCADE,
    -- The local date the events fall on, "YYYY-MM-DD" in the recipient's tz.
    digest_date  TEXT    NOT NULL,
    -- Unguessable /d/{token} segment. These links leave the network, so the
    -- token is the only thing protecting the page.
    token        TEXT    NOT NULL UNIQUE,
    events       TEXT    NOT NULL,
    created_at   TEXT    NOT NULL,
    -- First successful send. NULL means nobody was told, which is the only
    -- signal that a digest failed on every channel.
    notified_at  TEXT,
    -- Also the idempotency guard: it outlives River's job retention, so
    -- "already sent" does not expire when the job cleaner runs.
    UNIQUE (recipient_id, digest_date)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS digest_snapshots;
DROP TABLE IF EXISTS notification_targets;
DROP TABLE IF EXISTS recipients;
-- +goose StatementEnd
