-- +goose Up
ALTER TABLE releases ADD COLUMN package_omitted INTEGER NOT NULL DEFAULT 0
    CHECK (package_omitted IN (0, 1));

-- +goose Down
ALTER TABLE releases DROP COLUMN package_omitted;
