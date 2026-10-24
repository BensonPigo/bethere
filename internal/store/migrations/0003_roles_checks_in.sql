-- Whether a role checks in (M2c, ADR-0031; api.md section 3「新增角色」).
-- NULL follows the role type; 0 means the Master chose "no check-in".
-- Additive only (ADR-0019): a release without this column ignores it.
ALTER TABLE roles ADD COLUMN checks_in INTEGER CHECK (checks_in IS NULL OR checks_in = 0);
