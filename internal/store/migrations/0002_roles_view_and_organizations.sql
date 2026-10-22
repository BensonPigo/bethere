-- bethere:foreign_keys=off
-- Role types are checked in Go from now on, so a new type needs no table
-- rebuild (ADR-0016); this rebuild is the one exception of ADR-0019,
-- before any real data. Roles also get the screen they open (ADR-0025):
-- NULL means the type's default. Organizations are only reserved: one
-- organization, no interface (ADR-0016).

CREATE TABLE roles_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id      INTEGER NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    name          TEXT    NOT NULL,
    type          TEXT    NOT NULL,
    display_order INTEGER NOT NULL DEFAULT 0,
    view          TEXT
);
INSERT INTO roles_new (id, event_id, name, type, display_order, view)
    SELECT id, event_id, name, type, display_order, NULL FROM roles;
-- Keep the old AUTOINCREMENT counter: a deleted role's id must never be
-- given again (access-control.md section 5, rule 6).
DELETE FROM sqlite_sequence WHERE name = 'roles_new';
INSERT INTO sqlite_sequence (name, seq) SELECT 'roles_new', seq FROM sqlite_sequence WHERE name = 'roles';
DROP TABLE roles;
ALTER TABLE roles_new RENAME TO roles;
CREATE INDEX roles_event ON roles (event_id);
CREATE UNIQUE INDEX roles_one_master ON roles (event_id) WHERE type = 'Master';

CREATE TABLE organizations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);
ALTER TABLE events ADD COLUMN organization_id INTEGER REFERENCES organizations (id);
