package mfa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"durpdeploy/internal/db"

	"github.com/go-webauthn/webauthn/webauthn"
)

var ErrChallengeCSRF = errors.New("invalid MFA challenge CSRF")

type ResolvedChallenge struct {
	Binding      ChallengeBinding
	CeremonyJSON string
}

func (s *ChallengeService) Resolve(
	ctx context.Context,
	token string,
	csrf string,
	purpose ChallengePurpose,
) (ResolvedChallenge, error) {
	challenge, err := s.resolvePendingLogin(ctx, token, csrf)
	if err != nil {
		return ResolvedChallenge{}, err
	}
	if challenge.Purpose != string(purpose) {
		return ResolvedChallenge{}, ErrChallengeInvalid
	}
	return ResolvedChallenge{
		Binding: ChallengeBinding{
			Token: token, CSRF: csrf, UserID: challenge.UserID, Purpose: purpose,
		},
		CeremonyJSON: challenge.CeremonyJson,
	}, nil
}

func (s *ChallengeService) ResolveLogin(
	ctx context.Context,
	token string,
	csrf string,
) (ResolvedChallenge, error) {
	challenge, err := s.resolvePendingLogin(ctx, token, csrf)
	if err != nil {
		return ResolvedChallenge{}, err
	}
	purpose := ChallengePurpose(challenge.Purpose)
	if purpose != ChallengePurposeLoginMFA &&
		purpose != ChallengePurposeWebAuthnAuth {
		return ResolvedChallenge{}, ErrChallengeInvalid
	}
	return ResolvedChallenge{
		Binding: ChallengeBinding{
			Token: token, CSRF: csrf, UserID: challenge.UserID, Purpose: purpose,
		},
		CeremonyJSON: challenge.CeremonyJson,
	}, nil
}

func (s *ChallengeService) ResolveBound(
	ctx context.Context,
	binding ChallengeBinding,
) (ResolvedChallenge, error) {
	if binding.UserID <= 0 || binding.SessionID == "" ||
		!binding.Purpose.valid() {
		return ResolvedChallenge{}, ErrChallengeInvalid
	}
	now := s.clock().Unix()
	if err := cleanExpiredChallenges(ctx, s.repo.Queries, now); err != nil {
		return ResolvedChallenge{}, err
	}
	challenge, err := s.activeChallenge(ctx, s.repo.Queries, binding)
	if err != nil {
		return ResolvedChallenge{}, err
	}
	blocked, err := rateLimitBlocked(ctx, s.repo.Queries, rateLimitState{
		userID: binding.UserID, now: now,
	})
	if err != nil {
		return ResolvedChallenge{}, fmt.Errorf("read MFA rate limit: %w", err)
	}
	if blocked {
		return ResolvedChallenge{}, ErrMFACooldown
	}
	return ResolvedChallenge{
		Binding:      binding,
		CeremonyJSON: challenge.CeremonyJson,
	}, nil
}

func (s *ChallengeService) PromoteWebAuthn(
	ctx context.Context,
	binding ChallengeBinding,
	session webauthn.SessionData,
) (ChallengeBinding, error) {
	ceremony, err := json.Marshal(session)
	if err != nil {
		return ChallengeBinding{}, fmt.Errorf(
			"encode WebAuthn session data: %w",
			err,
		)
	}
	return s.Promote(
		ctx,
		binding,
		ChallengePurposeWebAuthnAuth,
		string(ceremony),
	)
}

func (s *ChallengeService) Promote(
	ctx context.Context,
	binding ChallengeBinding,
	nextPurpose ChallengePurpose,
	ceremonyJSON string,
) (ChallengeBinding, error) {
	if !canPromoteChallenge(binding.Purpose, nextPurpose) {
		return ChallengeBinding{}, ErrChallengeInvalid
	}
	now := s.clock().Unix()
	if err := cleanExpiredChallenges(ctx, s.repo.Queries, now); err != nil {
		return ChallengeBinding{}, err
	}
	err := s.repo.WithTx(ctx, func(queries *db.Queries) error {
		if err := lockMFAUser(ctx, queries, binding.UserID); err != nil {
			return err
		}
		blocked, err := rateLimitBlocked(ctx, queries, rateLimitState{
			userID: binding.UserID, now: now,
		})
		if err != nil {
			return err
		}
		if blocked {
			return ErrMFACooldown
		}
		updated, err := queries.PromoteMFAChallengeToWebAuthn(
			ctx,
			db.PromoteMFAChallengeToWebAuthnParams{
				TokenHash:    challengeHash(binding.Token),
				UserID:       binding.UserID,
				Purpose:      string(binding.Purpose),
				SessionID:    challengeSession(binding.SessionID),
				CsrfHash:     challengeHash(binding.CSRF),
				ExpiresAt:    now,
				Attempts:     maxFailures,
				NextPurpose:  string(nextPurpose),
				CeremonyJson: ceremonyJSON,
			},
		)
		if err != nil {
			return fmt.Errorf("promote MFA challenge: %w", err)
		}
		if updated != 1 {
			return ErrChallengeInvalid
		}
		return nil
	})
	if err != nil {
		return ChallengeBinding{}, err
	}
	binding.Purpose = nextPurpose
	return binding, nil
}

func (s *ChallengeService) Cancel(
	ctx context.Context,
	binding ChallengeBinding,
) error {
	return s.Consume(
		ctx,
		binding,
		func(context.Context, db.MfaChallenge) error {
			return nil
		},
	)
}

func canPromoteChallenge(
	current ChallengePurpose,
	next ChallengePurpose,
) bool {
	switch current {
	case ChallengePurposeLoginMFA, ChallengePurposeTOTPVerify:
		return next == ChallengePurposeWebAuthnAuth
	case ChallengePurposeTOTPEnroll:
		return next == ChallengePurposeTOTPVerify
	case ChallengePurposeWebAuthnRegister:
		return next == ChallengePurposeWebAuthnAuth
	default:
		return false
	}
}

func (s *ChallengeService) resolvePendingLogin(
	ctx context.Context,
	token string,
	csrf string,
) (db.MfaChallenge, error) {
	now := s.clock().Unix()
	if err := cleanExpiredChallenges(ctx, s.repo.Queries, now); err != nil {
		return db.MfaChallenge{}, err
	}
	challenge, err := s.repo.Queries.GetActiveMFAChallenge(
		ctx,
		db.GetActiveMFAChallengeParams{
			TokenHash: challengeHash(token), ExpiresAt: now,
		},
	)
	if err != nil {
		return db.MfaChallenge{}, ErrChallengeInvalid
	}
	if challenge.SessionID.Valid || !equalChallengeHash(
		challenge.CsrfHash,
		challengeHash(csrf),
	) {
		return db.MfaChallenge{}, ErrChallengeCSRF
	}
	blocked, err := rateLimitBlocked(ctx, s.repo.Queries, rateLimitState{
		userID: challenge.UserID, now: now,
	})
	if err != nil {
		return db.MfaChallenge{}, fmt.Errorf("read MFA rate limit: %w", err)
	}
	if blocked {
		return db.MfaChallenge{}, ErrMFACooldown
	}
	return challenge, nil
}
