package mfa

import (
	"errors"
	"io"
	"time"

	"durpdeploy/internal/repository"
)

const (
	challengeTTL = 5 * time.Minute
	maxFailures  = 5
)

var ErrChallengeInvalid = errors.New("invalid MFA challenge")

type ChallengePurpose string

const (
	ChallengePurposeLoginMFA         ChallengePurpose = "login_mfa"
	ChallengePurposeTOTPEnroll       ChallengePurpose = "totp_enroll"
	ChallengePurposeTOTPVerify       ChallengePurpose = "totp_verify"
	ChallengePurposeWebAuthnRegister ChallengePurpose = "webauthn_register"
	ChallengePurposeWebAuthnAuth     ChallengePurpose = "webauthn_auth"
	ChallengePurposeRecoveryVerify   ChallengePurpose = "recovery_verify"
	ChallengePurposeAdminMFAReset    ChallengePurpose = "admin_mfa_reset"
)

type ChallengeServiceConfig struct {
	Repository *repository.Repository
	Clock      func() time.Time
	Random     io.Reader
}

type ChallengeService struct {
	repo   *repository.Repository
	clock  func() time.Time
	random io.Reader
}

type ChallengeIssue struct {
	UserID       int64
	SessionID    string
	Purpose      ChallengePurpose
	CeremonyJSON string
}

type PendingChallenge struct {
	Token string
	CSRF  string
}

type ChallengeBinding struct {
	Token     string
	CSRF      string
	UserID    int64
	SessionID string
	Purpose   ChallengePurpose
}
