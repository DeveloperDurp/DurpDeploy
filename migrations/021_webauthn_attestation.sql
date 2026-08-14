-- +goose Up
-- +goose StatementBegin

ALTER TABLE webauthn_credentials ADD COLUMN attestation_type TEXT NOT NULL DEFAULT '';
ALTER TABLE webauthn_credentials ADD COLUMN attestation_format TEXT NOT NULL DEFAULT '';
ALTER TABLE webauthn_credentials ADD COLUMN attestation_client_data_json BLOB;
ALTER TABLE webauthn_credentials ADD COLUMN attestation_client_data_hash BLOB;
ALTER TABLE webauthn_credentials ADD COLUMN attestation_authenticator_data BLOB;
ALTER TABLE webauthn_credentials ADD COLUMN attestation_public_key_algorithm INTEGER NOT NULL DEFAULT 0;
ALTER TABLE webauthn_credentials ADD COLUMN attestation_object BLOB;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE webauthn_credentials DROP COLUMN attestation_object;
ALTER TABLE webauthn_credentials DROP COLUMN attestation_public_key_algorithm;
ALTER TABLE webauthn_credentials DROP COLUMN attestation_authenticator_data;
ALTER TABLE webauthn_credentials DROP COLUMN attestation_client_data_hash;
ALTER TABLE webauthn_credentials DROP COLUMN attestation_client_data_json;
ALTER TABLE webauthn_credentials DROP COLUMN attestation_format;
ALTER TABLE webauthn_credentials DROP COLUMN attestation_type;

-- +goose StatementEnd
