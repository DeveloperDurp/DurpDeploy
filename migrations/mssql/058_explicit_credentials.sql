-- +goose Up
ALTER TABLE variables ADD is_credential BIGINT NOT NULL CONSTRAINT df_variables_is_credential DEFAULT 0;
ALTER TABLE variables ADD CONSTRAINT ck_variables_is_credential CHECK (is_credential IN (0, 1) AND (is_credential = 0 OR secret <> 0));
ALTER TABLE lifecycle_variables ADD is_credential BIGINT NOT NULL CONSTRAINT df_lifecycle_variables_is_credential DEFAULT 0;
ALTER TABLE lifecycle_variables ADD CONSTRAINT ck_lifecycle_variables_is_credential CHECK (is_credential IN (0, 1) AND (is_credential = 0 OR secret <> 0));

-- +goose Down
ALTER TABLE variables DROP CONSTRAINT ck_variables_is_credential, df_variables_is_credential;
ALTER TABLE variables DROP COLUMN is_credential;
ALTER TABLE lifecycle_variables DROP CONSTRAINT ck_lifecycle_variables_is_credential, df_lifecycle_variables_is_credential;
ALTER TABLE lifecycle_variables DROP COLUMN is_credential;
