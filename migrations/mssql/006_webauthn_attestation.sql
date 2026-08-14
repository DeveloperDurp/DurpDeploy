-- +goose Up

ALTER TABLE webauthn_credentials ADD attestation_type NVARCHAR(64) NOT NULL CONSTRAINT df_webauthn_attestation_type DEFAULT '';
ALTER TABLE webauthn_credentials ADD attestation_format NVARCHAR(64) NOT NULL CONSTRAINT df_webauthn_attestation_format DEFAULT '';
ALTER TABLE webauthn_credentials ADD attestation_client_data_json VARBINARY(MAX) NULL;
ALTER TABLE webauthn_credentials ADD attestation_client_data_hash VARBINARY(MAX) NULL;
ALTER TABLE webauthn_credentials ADD attestation_authenticator_data VARBINARY(MAX) NULL;
ALTER TABLE webauthn_credentials ADD attestation_public_key_algorithm BIGINT NOT NULL CONSTRAINT df_webauthn_attestation_algorithm DEFAULT 0;
ALTER TABLE webauthn_credentials ADD attestation_object VARBINARY(MAX) NULL;

-- +goose Down

ALTER TABLE webauthn_credentials DROP CONSTRAINT df_webauthn_attestation_algorithm;
ALTER TABLE webauthn_credentials DROP CONSTRAINT df_webauthn_attestation_format;
ALTER TABLE webauthn_credentials DROP CONSTRAINT df_webauthn_attestation_type;
ALTER TABLE webauthn_credentials DROP COLUMN attestation_object;
ALTER TABLE webauthn_credentials DROP COLUMN attestation_public_key_algorithm;
ALTER TABLE webauthn_credentials DROP COLUMN attestation_authenticator_data;
ALTER TABLE webauthn_credentials DROP COLUMN attestation_client_data_hash;
ALTER TABLE webauthn_credentials DROP COLUMN attestation_client_data_json;
ALTER TABLE webauthn_credentials DROP COLUMN attestation_format;
ALTER TABLE webauthn_credentials DROP COLUMN attestation_type;
