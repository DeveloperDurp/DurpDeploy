-- +goose Up
-- +goose StatementBegin

CREATE TABLE oidc_identities (
    issuer NVARCHAR(512) NOT NULL CHECK (LEN(issuer) <= 512),
    subject NVARCHAR(255) NOT NULL CHECK (LEN(subject) <= 255),
    user_id BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    created_at BIGINT NOT NULL DEFAULT DATEDIFF_BIG(SECOND, '1970-01-01', SYSUTCDATETIME()),
    CONSTRAINT uq_oidc_identities_issuer_subject UNIQUE (issuer, subject)
);

CREATE TABLE oidc_transactions (
    state_hash NVARCHAR(64) NOT NULL PRIMARY KEY CHECK (
        LEN(state_hash) = 64
        AND REPLACE(
            REPLACE(
                REPLACE(
                    REPLACE(
                        REPLACE(
                            REPLACE(
                                REPLACE(
                                    REPLACE(
                                        REPLACE(
                                            REPLACE(
                                                REPLACE(
                                                    REPLACE(
                                                        REPLACE(
                                                            REPLACE(
                                                                REPLACE(
                                                                    REPLACE(
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
    expires_at BIGINT NOT NULL
);

CREATE INDEX idx_oidc_transactions_expires_at
    ON oidc_transactions(expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE oidc_transactions;
DROP TABLE oidc_identities;

-- +goose StatementEnd
