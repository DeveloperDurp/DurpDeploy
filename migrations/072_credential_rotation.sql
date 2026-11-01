-- +goose Up
ALTER TABLE variables ADD COLUMN expires_at INTEGER NOT NULL DEFAULT 0 CHECK (expires_at >= 0);
ALTER TABLE variables ADD COLUMN rotation_due_at INTEGER NOT NULL DEFAULT 0 CHECK (rotation_due_at >= 0);
ALTER TABLE variables ADD COLUMN rotated_at INTEGER NOT NULL DEFAULT 0 CHECK (rotated_at >= 0);
ALTER TABLE variables ADD COLUMN rotation_revision INTEGER NOT NULL DEFAULT 0 CHECK (rotation_revision >= 0);
ALTER TABLE lifecycle_variables ADD COLUMN expires_at INTEGER NOT NULL DEFAULT 0 CHECK (expires_at >= 0);
ALTER TABLE lifecycle_variables ADD COLUMN rotation_due_at INTEGER NOT NULL DEFAULT 0 CHECK (rotation_due_at >= 0);
ALTER TABLE lifecycle_variables ADD COLUMN rotated_at INTEGER NOT NULL DEFAULT 0 CHECK (rotated_at >= 0);
ALTER TABLE lifecycle_variables ADD COLUMN rotation_revision INTEGER NOT NULL DEFAULT 0 CHECK (rotation_revision >= 0);
ALTER TABLE package_repositories ADD COLUMN expires_at INTEGER NOT NULL DEFAULT 0 CHECK (expires_at >= 0);
ALTER TABLE package_repositories ADD COLUMN rotation_due_at INTEGER NOT NULL DEFAULT 0 CHECK (rotation_due_at >= 0);
ALTER TABLE package_repositories ADD COLUMN rotated_at INTEGER NOT NULL DEFAULT 0 CHECK (rotated_at >= 0);
ALTER TABLE package_repositories ADD COLUMN rotation_revision INTEGER NOT NULL DEFAULT 0 CHECK (rotation_revision >= 0);

-- +goose Down
ALTER TABLE variables DROP COLUMN expires_at;
ALTER TABLE variables DROP COLUMN rotation_due_at;
ALTER TABLE variables DROP COLUMN rotated_at;
ALTER TABLE variables DROP COLUMN rotation_revision;
ALTER TABLE lifecycle_variables DROP COLUMN expires_at;
ALTER TABLE lifecycle_variables DROP COLUMN rotation_due_at;
ALTER TABLE lifecycle_variables DROP COLUMN rotated_at;
ALTER TABLE lifecycle_variables DROP COLUMN rotation_revision;
ALTER TABLE package_repositories DROP COLUMN expires_at;
ALTER TABLE package_repositories DROP COLUMN rotation_due_at;
ALTER TABLE package_repositories DROP COLUMN rotated_at;
ALTER TABLE package_repositories DROP COLUMN rotation_revision;
