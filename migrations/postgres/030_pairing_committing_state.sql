-- +goose Up
-- +goose StatementBegin

ALTER TABLE agent_pairings DROP CONSTRAINT agent_pairings_state_check;
ALTER TABLE agent_pairings ADD CONSTRAINT agent_pairings_state_check
    CHECK (state IN ('pending', 'committing', 'paired', 'expired'));
ALTER TABLE agent_pairings DROP CONSTRAINT agent_pairings_check;
ALTER TABLE agent_pairings ADD CONSTRAINT agent_pairings_check CHECK (
    (state IN ('pending', 'committing', 'expired')
        AND server_public_identity IS NULL AND server_pin IS NULL AND paired_at IS NULL)
    OR (state = 'paired' AND server_public_identity IS NOT NULL
        AND server_pin IS NOT NULL AND paired_at IS NOT NULL)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM agent_pairings WHERE state = 'committing';
ALTER TABLE agent_pairings DROP CONSTRAINT agent_pairings_state_check;
ALTER TABLE agent_pairings ADD CONSTRAINT agent_pairings_state_check
    CHECK (state IN ('pending', 'paired', 'expired'));
ALTER TABLE agent_pairings DROP CONSTRAINT agent_pairings_check;
ALTER TABLE agent_pairings ADD CONSTRAINT agent_pairings_check CHECK (
    (state = 'pending' AND server_public_identity IS NULL
        AND server_pin IS NULL AND paired_at IS NULL)
    OR (state = 'paired' AND server_public_identity IS NOT NULL
        AND server_pin IS NOT NULL AND paired_at IS NOT NULL)
    OR (state = 'expired' AND server_public_identity IS NULL
        AND server_pin IS NULL AND paired_at IS NULL)
);

-- +goose StatementEnd
