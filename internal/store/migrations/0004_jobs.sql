-- The four jobs (api.md section 9, ADR-0035): a venue lead's acks, the
-- runners' task pool, the reception's guest list and the cashier's
-- checklist. Additive only (ADR-0019): a release without these tables
-- ignores them. Everything goes with its event (NFR-5).

CREATE TABLE cue_acks (
    cue_id  INTEGER NOT NULL REFERENCES cues (id) ON DELETE CASCADE,
    role_id INTEGER NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    at      INTEGER NOT NULL,
    PRIMARY KEY (cue_id, role_id)
);
CREATE INDEX cue_acks_role ON cue_acks (role_id);

CREATE TABLE tasks (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id           INTEGER NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    title              TEXT    NOT NULL,
    created_by_role_id INTEGER REFERENCES roles (id) ON DELETE SET NULL,
    created_at         INTEGER NOT NULL,
    claimed_by_role_id INTEGER REFERENCES roles (id) ON DELETE SET NULL,
    state              TEXT    NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'claimed', 'done')),
    updated_at         INTEGER NOT NULL
);
CREATE INDEX tasks_event ON tasks (event_id);

CREATE TABLE guests (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id    INTEGER NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    name        TEXT    NOT NULL,
    table_label TEXT    NOT NULL,
    note        TEXT,
    arrived_at  INTEGER
);
CREATE INDEX guests_event ON guests (event_id);

CREATE TABLE checklist_items (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id        INTEGER NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    seq             INTEGER NOT NULL,
    title           TEXT    NOT NULL,
    done_at         INTEGER,
    done_by_role_id INTEGER REFERENCES roles (id) ON DELETE SET NULL
);
CREATE INDEX checklist_items_event ON checklist_items (event_id);
