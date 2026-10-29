-- +goose Up
ALTER TABLE variables ADD expires_at BIGINT NOT NULL CONSTRAINT df_variables_expires_at DEFAULT 0 CONSTRAINT ck_variables_expires_at CHECK (expires_at >= 0);
ALTER TABLE variables ADD rotation_due_at BIGINT NOT NULL CONSTRAINT df_variables_rotation_due_at DEFAULT 0 CONSTRAINT ck_variables_rotation_due_at CHECK (rotation_due_at >= 0);
ALTER TABLE variables ADD rotated_at BIGINT NOT NULL CONSTRAINT df_variables_rotated_at DEFAULT 0 CONSTRAINT ck_variables_rotated_at CHECK (rotated_at >= 0);
ALTER TABLE variables ADD rotation_revision BIGINT NOT NULL CONSTRAINT df_variables_rotation_revision DEFAULT 0 CONSTRAINT ck_variables_rotation_revision CHECK (rotation_revision >= 0);
ALTER TABLE lifecycle_variables ADD expires_at BIGINT NOT NULL CONSTRAINT df_lifecycle_variables_expires_at DEFAULT 0 CONSTRAINT ck_lifecycle_variables_expires_at CHECK (expires_at >= 0);
ALTER TABLE lifecycle_variables ADD rotation_due_at BIGINT NOT NULL CONSTRAINT df_lifecycle_variables_rotation_due_at DEFAULT 0 CONSTRAINT ck_lifecycle_variables_rotation_due_at CHECK (rotation_due_at >= 0);
ALTER TABLE lifecycle_variables ADD rotated_at BIGINT NOT NULL CONSTRAINT df_lifecycle_variables_rotated_at DEFAULT 0 CONSTRAINT ck_lifecycle_variables_rotated_at CHECK (rotated_at >= 0);
ALTER TABLE lifecycle_variables ADD rotation_revision BIGINT NOT NULL CONSTRAINT df_lifecycle_variables_rotation_revision DEFAULT 0 CONSTRAINT ck_lifecycle_variables_rotation_revision CHECK (rotation_revision >= 0);
ALTER TABLE package_repositories ADD expires_at BIGINT NOT NULL CONSTRAINT df_package_repositories_expires_at DEFAULT 0 CONSTRAINT ck_package_repositories_expires_at CHECK (expires_at >= 0);
ALTER TABLE package_repositories ADD rotation_due_at BIGINT NOT NULL CONSTRAINT df_package_repositories_rotation_due_at DEFAULT 0 CONSTRAINT ck_package_repositories_rotation_due_at CHECK (rotation_due_at >= 0);
ALTER TABLE package_repositories ADD rotated_at BIGINT NOT NULL CONSTRAINT df_package_repositories_rotated_at DEFAULT 0 CONSTRAINT ck_package_repositories_rotated_at CHECK (rotated_at >= 0);
ALTER TABLE package_repositories ADD rotation_revision BIGINT NOT NULL CONSTRAINT df_package_repositories_rotation_revision DEFAULT 0 CONSTRAINT ck_package_repositories_rotation_revision CHECK (rotation_revision >= 0);

-- +goose Down
ALTER TABLE variables DROP CONSTRAINT df_variables_expires_at, ck_variables_expires_at;
ALTER TABLE variables DROP COLUMN expires_at;
ALTER TABLE variables DROP CONSTRAINT df_variables_rotation_due_at, ck_variables_rotation_due_at;
ALTER TABLE variables DROP COLUMN rotation_due_at;
ALTER TABLE variables DROP CONSTRAINT df_variables_rotated_at, ck_variables_rotated_at;
ALTER TABLE variables DROP COLUMN rotated_at;
ALTER TABLE variables DROP CONSTRAINT df_variables_rotation_revision, ck_variables_rotation_revision;
ALTER TABLE variables DROP COLUMN rotation_revision;
ALTER TABLE lifecycle_variables DROP CONSTRAINT df_lifecycle_variables_expires_at, ck_lifecycle_variables_expires_at;
ALTER TABLE lifecycle_variables DROP COLUMN expires_at;
ALTER TABLE lifecycle_variables DROP CONSTRAINT df_lifecycle_variables_rotation_due_at, ck_lifecycle_variables_rotation_due_at;
ALTER TABLE lifecycle_variables DROP COLUMN rotation_due_at;
ALTER TABLE lifecycle_variables DROP CONSTRAINT df_lifecycle_variables_rotated_at, ck_lifecycle_variables_rotated_at;
ALTER TABLE lifecycle_variables DROP COLUMN rotated_at;
ALTER TABLE lifecycle_variables DROP CONSTRAINT df_lifecycle_variables_rotation_revision, ck_lifecycle_variables_rotation_revision;
ALTER TABLE lifecycle_variables DROP COLUMN rotation_revision;
ALTER TABLE package_repositories DROP CONSTRAINT df_package_repositories_expires_at, ck_package_repositories_expires_at;
ALTER TABLE package_repositories DROP COLUMN expires_at;
ALTER TABLE package_repositories DROP CONSTRAINT df_package_repositories_rotation_due_at, ck_package_repositories_rotation_due_at;
ALTER TABLE package_repositories DROP COLUMN rotation_due_at;
ALTER TABLE package_repositories DROP CONSTRAINT df_package_repositories_rotated_at, ck_package_repositories_rotated_at;
ALTER TABLE package_repositories DROP COLUMN rotated_at;
ALTER TABLE package_repositories DROP CONSTRAINT df_package_repositories_rotation_revision, ck_package_repositories_rotation_revision;
ALTER TABLE package_repositories DROP COLUMN rotation_revision;
