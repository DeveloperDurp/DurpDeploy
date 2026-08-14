package mfa

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"time"

	"durpdeploy/internal/db"
)

func NewChallengeService(config ChallengeServiceConfig) *ChallengeService {
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	return &ChallengeService{
		repo: config.Repository, clock: config.Clock, random: config.Random,
	}
}

func (s *ChallengeService) Issue(
	ctx context.Context,
	issue ChallengeIssue,
) (PendingChallenge, error) {
	if issue.UserID <= 0 || !issue.Purpose.valid() {
		return PendingChallenge{}, ErrChallengeInvalid
	}
	token, err := randomChallengeValue(s.random)
	if err != nil {
		return PendingChallenge{}, fmt.Errorf(
			"generate MFA challenge token: %w",
			err,
		)
	}
	csrf, err := randomChallengeValue(s.random)
	if err != nil {
		return PendingChallenge{}, fmt.Errorf(
			"generate MFA challenge CSRF: %w",
			err,
		)
	}
	now := s.clock().Unix()
	err = s.repo.WithTx(ctx, func(queries *db.Queries) error {
		if err := cleanExpiredChallenges(ctx, queries, now); err != nil {
			return err
		}
		_, err := queries.CreateMFAChallenge(ctx, db.CreateMFAChallengeParams{
			TokenHash:    challengeHash(token),
			UserID:       issue.UserID,
			SessionID:    challengeSession(issue.SessionID),
			Purpose:      string(issue.Purpose),
			CsrfHash:     challengeHash(csrf),
			CeremonyJson: issue.CeremonyJSON,
			ExpiresAt:    now + int64(challengeTTL/time.Second),
		})
		if err != nil {
			return fmt.Errorf("create MFA challenge: %w", err)
		}
		return nil
	})
	if err != nil {
		return PendingChallenge{}, err
	}
	return PendingChallenge{Token: token, CSRF: csrf}, nil
}

func cleanExpiredChallenges(
	ctx context.Context,
	queries *db.Queries,
	now int64,
) error {
	if err := queries.DeleteExpiredMFAChallenges(ctx, now+1); err != nil {
		return fmt.Errorf("clean expired MFA challenges: %w", err)
	}
	return nil
}

func randomChallengeValue(random io.Reader) (string, error) {
	value := make([]byte, sha256.Size)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func challengeHash(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func challengeSession(sessionID string) sql.NullString {
	if sessionID == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: sessionID, Valid: true}
}

func equalChallengeHash(first, second []byte) bool {
	if len(first) != sha256.Size || len(second) != sha256.Size {
		return false
	}
	return subtle.ConstantTimeCompare(first, second) == 1
}

func (p ChallengePurpose) valid() bool {
	switch p {
	case ChallengePurposeLoginMFA,
		ChallengePurposeTOTPEnroll,
		ChallengePurposeTOTPVerify,
		ChallengePurposeWebAuthnRegister,
		ChallengePurposeWebAuthnAuth,
		ChallengePurposeRecoveryVerify,
		ChallengePurposeAdminMFAReset:
		return true
	}
	return false
}
