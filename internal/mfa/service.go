package mfa

import (
	"crypto/rand"

	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

// Service owns the validated MFA configuration and encryption dependency.
// Protocol-specific behavior is added only when its handlers are implemented.
type Service struct {
	config Config
	box    *secret.Box
}

func NewService(config Config, box *secret.Box) *Service {
	return &Service{config: config, box: box}
}

func (s *Service) CookieSecure() bool {
	return s.config.CookieSecure
}

func (s *Service) Challenges(repo *repository.Repository) *ChallengeService {
	return NewChallengeService(ChallengeServiceConfig{Repository: repo})
}

func (s *Service) Factors(repo *repository.Repository) *FactorStore {
	return NewFactorStore(FactorStoreConfig{
		Repository:     repo,
		Box:            s.box,
		TOTP:           NewTOTP(nil, nil),
		RecoveryRandom: rand.Reader,
	})
}

func (s *Service) WebAuthnAdapter() (*WebAuthnAdapter, error) {
	return NewWebAuthnAdapter(s.config.WebAuthn)
}
