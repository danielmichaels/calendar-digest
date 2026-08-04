-- +goose Up
-- app_state holds the few facts the process needs to remember across restarts
-- that belong to no other table.
--
-- It exists for one reason: an alert that fires on a transition needs to know
-- what the previous state was, and a flag in memory reports "restored" every
-- time the process starts after a failure — the one moment it is least true.
CREATE TABLE app_state
(
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE app_state;
