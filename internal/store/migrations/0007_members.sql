-- Wedding planner companies (M4, ADR-0039; data-model.md「婚顧公司」).
-- organizations exists since 0002. A member is one email of one company;
-- removing a member deletes the row, and its login and console links go
-- with it through ON DELETE CASCADE. A console link is a member's way into
-- one event as its Master; it goes with the event too. Ids that the
-- action log names (members, console_tokens) use AUTOINCREMENT so they
-- are never given again.
-- Additive only (ADR-0019): a release without them ignores the tables.

CREATE TABLE members (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    organization_id INTEGER NOT NULL REFERENCES organizations (id),
    email           TEXT    NOT NULL UNIQUE,
    created_at      INTEGER NOT NULL
);
CREATE INDEX members_organization ON members (organization_id);

CREATE TABLE member_sessions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    member_id  INTEGER NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    token_hash BLOB    NOT NULL UNIQUE CHECK (length(token_hash) = 32), -- SHA-256
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX member_sessions_member ON member_sessions (member_id, id);

CREATE TABLE console_tokens (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    member_id  INTEGER NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    event_id   INTEGER NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    token_hash BLOB    NOT NULL UNIQUE CHECK (length(token_hash) = 32), -- SHA-256
    created_at INTEGER NOT NULL
);
CREATE INDEX console_tokens_member_event ON console_tokens (member_id, event_id, id);
CREATE INDEX console_tokens_event ON console_tokens (event_id);
CREATE INDEX events_organization ON events (organization_id, created_at);
