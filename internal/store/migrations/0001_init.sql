-- P0 schema (docs/design/data-model.md). Times are Unix nanoseconds, UTC.
-- Frozen once deployed: later migrations only add tables, nullable columns
-- or indexes (tech-stack.md T4). IDs that identify something across
-- requests (events, roles, access_tokens, cues) use AUTOINCREMENT so they
-- are never given again after a purge; check_ins and action_logs ids stay
-- internal.

CREATE TABLE events (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    name               TEXT    NOT NULL,
    event_date         TEXT    NOT NULL,              -- YYYY-MM-DD in timezone
    timezone           TEXT    NOT NULL,
    status             TEXT    NOT NULL CHECK (status IN ('Draft', 'Live')),
    live_started_at    INTEGER,
    recovery_code_hash BLOB    NOT NULL UNIQUE CHECK (length(recovery_code_hash) = 32), -- SHA-256
    version            INTEGER NOT NULL DEFAULT 1,
    created_at         INTEGER NOT NULL
);

CREATE TABLE roles (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id      INTEGER NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    name          TEXT    NOT NULL,
    type          TEXT    NOT NULL CHECK (type IN ('Master', 'Stylist', 'Media', 'StageAV',
                                  'VenueLead', 'Reception', 'Cashier', 'Runner', 'Host')),
    display_order INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX roles_event ON roles (event_id);
CREATE UNIQUE INDEX roles_one_master ON roles (event_id) WHERE type = 'Master';

CREATE TABLE access_tokens (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    role_id    INTEGER NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    token_hash BLOB    NOT NULL UNIQUE CHECK (length(token_hash) = 32), -- SHA-256
    created_at INTEGER NOT NULL,
    revoked_at INTEGER
);
CREATE UNIQUE INDEX access_tokens_one_active ON access_tokens (role_id) WHERE revoked_at IS NULL;

CREATE TABLE cues (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id        INTEGER NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    seq             INTEGER NOT NULL,
    title           TEXT    NOT NULL,
    scheduled_start INTEGER NOT NULL,
    duration_min    INTEGER NOT NULL CHECK (duration_min >= 0),
    is_fixed        INTEGER NOT NULL DEFAULT 0,
    offset_min      INTEGER NOT NULL DEFAULT 0,
    status          TEXT    NOT NULL DEFAULT 'Pending'
                            CHECK (status IN ('Pending', 'InProgress', 'Completed', 'Skipped')),
    actual_start    INTEGER,
    actual_end      INTEGER,
    note_public     TEXT,
    UNIQUE (event_id, seq)
);
-- At most one cue in progress per event (data-model.md M3).
CREATE UNIQUE INDEX cues_one_in_progress ON cues (event_id) WHERE status = 'InProgress';

CREATE TABLE cue_roles (
    cue_id    INTEGER NOT NULL REFERENCES cues (id) ON DELETE CASCADE,
    role_id   INTEGER NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    role_note TEXT,
    lead_min  INTEGER,
    PRIMARY KEY (cue_id, role_id)
);
CREATE INDEX cue_roles_role ON cue_roles (role_id);

CREATE TABLE check_ins (
    id         INTEGER PRIMARY KEY,
    role_id    INTEGER NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    at         INTEGER NOT NULL,
    by_role_id INTEGER NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    undone_at  INTEGER
);
CREATE INDEX check_ins_role ON check_ins (role_id);

-- Audit trail for every state change. Cue operations (types "cue.*") carry
-- their before/after cue states in payload so the latest one can be undone
-- exactly; this replaces ShiftLog/ShiftLogEntry (ADR-0015).
CREATE TABLE action_logs (
    id        INTEGER PRIMARY KEY,
    event_id  INTEGER NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    at        INTEGER NOT NULL,
    actor     TEXT    NOT NULL,
    type      TEXT    NOT NULL,
    payload   TEXT    NOT NULL DEFAULT '{}',
    undone_at INTEGER
);
CREATE INDEX action_logs_event ON action_logs (event_id, id);
