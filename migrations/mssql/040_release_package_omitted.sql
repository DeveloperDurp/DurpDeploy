-- +goose Up
ALTER TABLE releases ADD package_omitted BIGINT NOT NULL
    CONSTRAINT df_releases_package_omitted DEFAULT 0
    CONSTRAINT ck_releases_package_omitted CHECK (package_omitted IN (0, 1));

-- +goose Down
ALTER TABLE releases DROP CONSTRAINT ck_releases_package_omitted;
ALTER TABLE releases DROP CONSTRAINT df_releases_package_omitted;
ALTER TABLE releases DROP COLUMN package_omitted;
