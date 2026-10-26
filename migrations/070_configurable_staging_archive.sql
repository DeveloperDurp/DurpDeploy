-- +goose Up
-- Replace only size. Preserve snapshot keys, hashes, chunks, and foreign keys.
CREATE TABLE staging_archive_sizes AS
    SELECT deployment_id, step_index, producer, size FROM deployment_staging_snapshots;
ALTER TABLE deployment_staging_snapshots DROP COLUMN size;
ALTER TABLE deployment_staging_snapshots ADD COLUMN size INTEGER NOT NULL DEFAULT 1
    CHECK (size > 0);
UPDATE deployment_staging_snapshots SET size = (
    SELECT old.size FROM staging_archive_sizes old
    WHERE old.deployment_id = deployment_staging_snapshots.deployment_id
      AND old.step_index = deployment_staging_snapshots.step_index
      AND old.producer = deployment_staging_snapshots.producer
);
DROP TABLE staging_archive_sizes;

-- +goose Down
-- Restore the old ceiling only when every saved size fits. Goose rolls back
-- this transaction if restoring any larger size violates the old constraint.
CREATE TABLE staging_archive_sizes AS
    SELECT deployment_id, step_index, producer, size FROM deployment_staging_snapshots;
ALTER TABLE deployment_staging_snapshots DROP COLUMN size;
ALTER TABLE deployment_staging_snapshots ADD COLUMN size INTEGER NOT NULL DEFAULT 1
    CHECK (size > 0 AND size <= 570425344);
UPDATE deployment_staging_snapshots SET size = (
    SELECT old.size FROM staging_archive_sizes old
    WHERE old.deployment_id = deployment_staging_snapshots.deployment_id
      AND old.step_index = deployment_staging_snapshots.step_index
      AND old.producer = deployment_staging_snapshots.producer
);
DROP TABLE staging_archive_sizes;
