-- Members log in with a login mail (M5, ADR-0040; data-model.md LoginMail).
-- A row is one login mail, sent or failed: the limits of US-CO-1.11 count
-- the rows of the last 24 hours, so a failed one counts too and the daily
-- purge deletes them after a day. A removed member's rows stay with
-- member_id NULL, so the company's and the system's counts never drop;
-- the restore reset clears token_hash only.
-- verified is 1 on every login and console link this release makes and
-- NULL on those of an email-only release, which this one refuses
-- (US-CO-1.13). It has no default on purpose: an email-only release's
-- INSERT leaves it NULL.
-- Additive only (ADR-0019): a release without them ignores the table and
-- the columns.

CREATE TABLE login_mails (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    member_id       INTEGER REFERENCES members (id) ON DELETE SET NULL,
    organization_id INTEGER NOT NULL REFERENCES organizations (id),
    token_hash      BLOB    UNIQUE CHECK (token_hash IS NULL OR length(token_hash) = 32), -- SHA-256
    created_at      INTEGER NOT NULL,
    expires_at      INTEGER NOT NULL,
    used_at         INTEGER
);
CREATE INDEX login_mails_created ON login_mails (created_at);
CREATE INDEX login_mails_member ON login_mails (member_id, created_at);
CREATE INDEX login_mails_organization ON login_mails (organization_id, created_at);

ALTER TABLE member_sessions ADD COLUMN verified INTEGER;
ALTER TABLE console_tokens ADD COLUMN verified INTEGER;
