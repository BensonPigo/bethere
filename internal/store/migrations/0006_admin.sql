-- The admin page (US-O-9, US-O-10, ADR-0038). admin_tokens holds the one
-- admin link: issuing a new one deletes the last. admin_invites records
-- when the admin page made a code, kept apart from invites (a used code
-- is deleted), so the daily limit counts every code it made.
-- Additive only (ADR-0019): a release without them ignores the tables.

CREATE TABLE admin_tokens (
    token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32) -- SHA-256
);

CREATE TABLE admin_invites (
    at INTEGER NOT NULL
);
CREATE INDEX admin_invites_at ON admin_invites (at);
