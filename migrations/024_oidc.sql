-- +goose Up
-- +goose StatementBegin

CREATE TABLE oidc_identities (
    issuer TEXT NOT NULL CHECK (length(issuer) <= 512),
    subject TEXT NOT NULL CHECK (length(subject) <= 255),
    user_id INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE (issuer, subject)
);

CREATE TABLE oidc_transactions (
    state_hash TEXT PRIMARY KEY NOT NULL CHECK (
        length(state_hash) = 64
        AND replace(
            replace(
                replace(
                    replace(
                        replace(
                            replace(
                                replace(
                                    replace(
                                        replace(
                                            replace(
                                                replace(
                                                    replace(
                                                        replace(
                                                            replace(
                                                                replace(
                                                                    replace(
                                                                        state_hash,
                                                                        '0',
                                                                        ''
                                                                    ),
                                                                    '1',
                                                                    ''
                                                                ),
                                                                '2',
                                                                ''
                                                            ),
                                                            '3',
                                                            ''
                                                        ),
                                                        '4',
                                                        ''
                                                    ),
                                                    '5',
                                                    ''
                                                ),
                                                '6',
                                                ''
                                            ),
                                            '7',
                                            ''
                                        ),
                                        '8',
                                        ''
                                    ),
                                    '9',
                                    ''
                                ),
                                'a',
                                ''
                            ),
                            'b',
                            ''
                        ),
                        'c',
                        ''
                    ),
                    'd',
                    ''
                ),
                'e',
                ''
            ),
            'f',
            ''
        ) = ''
    ),
    expires_at INTEGER NOT NULL
);

CREATE INDEX idx_oidc_transactions_expires_at
    ON oidc_transactions(expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE oidc_transactions;
DROP TABLE oidc_identities;

-- +goose StatementEnd
