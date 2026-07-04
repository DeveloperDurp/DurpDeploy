-- +goose Up
-- +goose StatementBegin

ALTER TABLE deployments ADD COLUMN note TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- SQLite <3.35 cannot DROP COLUMN. Leave the column in place on Down;
-- matches the precedent set in 003_lifecycles.sql and 005_step_timeout.sql.

-- +goose StatementEnd
