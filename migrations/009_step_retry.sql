-- +goose Up
-- +goose StatementBegin

ALTER TABLE steps ADD COLUMN max_retries INTEGER NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- SQLite <3.35 cannot DROP COLUMN. Leave the column in place on Down;
-- matches the precedent set in 003_lifecycles.sql, 005_step_timeout.sql, 006.

-- +goose StatementEnd
