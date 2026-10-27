-- Invite codes (US-M-17, ADR-0036): each creates one event on /new. A used
-- code is deleted in the transaction that creates its event, an expired
-- one by the daily purge, so the table holds only codes still usable.
-- Additive only (ADR-0019): a release without it ignores the table.

CREATE TABLE invites (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    code_hash  BLOB    NOT NULL UNIQUE CHECK (length(code_hash) = 32), -- SHA-256
    expires_at INTEGER NOT NULL
);
