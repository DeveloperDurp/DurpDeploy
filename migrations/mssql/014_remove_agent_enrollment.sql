-- +goose Up
-- +goose StatementBegin

DROP TABLE agent_enrollment_tokens;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE agent_enrollment_tokens (
    token_hash BINARY(32) NOT NULL PRIMARY KEY,
    agent_id NVARCHAR(255) NOT NULL REFERENCES agents(id) ON DELETE NO ACTION,
    token_prefix NVARCHAR(16) NOT NULL CHECK (LEN(token_prefix) <= 16),
    expires_at BIGINT NOT NULL,
    used_at BIGINT NULL,
    revoked_at BIGINT NULL,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME())
);
CREATE INDEX idx_agent_enrollment_tokens_agent_id
    ON agent_enrollment_tokens(agent_id);
CREATE INDEX idx_agent_enrollment_tokens_expires_at
    ON agent_enrollment_tokens(expires_at);

-- +goose StatementEnd
