-- +goose Up
-- Existing secrets require explicit enrollment, including secrets with dates.
ALTER TABLE variables ADD COLUMN is_credential INTEGER NOT NULL DEFAULT 0 CHECK (is_credential IN (0, 1) AND (is_credential = 0 OR secret <> 0));
ALTER TABLE lifecycle_variables ADD COLUMN is_credential INTEGER NOT NULL DEFAULT 0 CHECK (is_credential IN (0, 1) AND (is_credential = 0 OR secret <> 0));

-- +goose Down
ALTER TABLE variables DROP COLUMN is_credential;
ALTER TABLE lifecycle_variables DROP COLUMN is_credential;
