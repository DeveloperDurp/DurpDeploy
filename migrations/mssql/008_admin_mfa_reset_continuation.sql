-- +goose Up
-- +goose StatementBegin

DECLARE @constraint_name NVARCHAR(128);
SELECT @constraint_name = name
FROM sys.check_constraints
WHERE parent_object_id = OBJECT_ID('mfa_challenges')
  AND definition LIKE '%totp_enroll%';
IF @constraint_name IS NOT NULL
    EXEC('ALTER TABLE mfa_challenges DROP CONSTRAINT [' + @constraint_name + ']');
ALTER TABLE mfa_challenges ADD CONSTRAINT ck_mfa_challenges_purpose CHECK (purpose IN (
    'login_mfa', 'totp_enroll', 'totp_verify', 'webauthn_register',
    'webauthn_auth', 'recovery_verify', 'admin_mfa_reset'
));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM mfa_challenges WHERE purpose = 'admin_mfa_reset';
DECLARE @constraint_name NVARCHAR(128);
SELECT @constraint_name = name
FROM sys.check_constraints
WHERE parent_object_id = OBJECT_ID('mfa_challenges')
  AND definition LIKE '%totp_enroll%';
IF @constraint_name IS NOT NULL
    EXEC('ALTER TABLE mfa_challenges DROP CONSTRAINT [' + @constraint_name + ']');
ALTER TABLE mfa_challenges ADD CONSTRAINT ck_mfa_challenges_purpose CHECK (purpose IN (
    'login_mfa', 'totp_enroll', 'totp_verify', 'webauthn_register',
    'webauthn_auth', 'recovery_verify'
));
-- +goose StatementEnd
