package agentserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

type pairingChallenge struct {
	public PairingChallenge
	input  PairingInput
}

func (service *PairingService) Begin(
	ctx context.Context,
	input PairingStartInput,
) (PairingChallenge, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	connection, err := service.connect(requestCtx, input.address, service.identity)
	if err != nil {
		return PairingChallenge{}, fmt.Errorf("observe agent certificate: %w", err)
	}
	defer connection.Close()
	fingerprint := agenttls.FingerprintOf(connection.CertificateDER())
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return PairingChallenge{}, fmt.Errorf("create pairing confirmation: %w", err)
	}
	challenge := PairingChallenge{
		ID: base64.RawURLEncoding.EncodeToString(token), Name: input.name,
		Address: input.address, Fingerprint: fingerprint.String(),
		ExpiresAt: service.now().Add(pairingTTL),
	}
	service.challengeMu.Lock()
	defer service.challengeMu.Unlock()
	service.pruneChallenges()
	service.challenges[challenge.ID] = pairingChallenge{
		public: challenge,
		input: PairingInput{
			name: input.name, address: input.address, code: input.code,
			fingerprint: fingerprint,
		},
	}
	return challenge, nil
}

func (service *PairingService) Challenge(id string) (PairingChallenge, error) {
	service.challengeMu.Lock()
	defer service.challengeMu.Unlock()
	service.pruneChallenges()
	challenge, ok := service.challenges[id]
	if !ok {
		return PairingChallenge{}, ErrPairingChallenge
	}
	return challenge.public, nil
}

func (service *PairingService) Approve(
	ctx context.Context,
	id string,
) (PairingResult, error) {
	service.challengeMu.Lock()
	service.pruneChallenges()
	challenge, ok := service.challenges[id]
	if ok {
		delete(service.challenges, id)
	}
	service.challengeMu.Unlock()
	if !ok {
		return PairingResult{}, ErrPairingChallenge
	}
	return service.Pair(ctx, challenge.input)
}

func (service *PairingService) Deny(id string) error {
	service.challengeMu.Lock()
	defer service.challengeMu.Unlock()
	service.pruneChallenges()
	if _, ok := service.challenges[id]; !ok {
		return ErrPairingChallenge
	}
	delete(service.challenges, id)
	return nil
}

func (service *PairingService) pruneChallenges() {
	now := service.now()
	for id, challenge := range service.challenges {
		if !challenge.public.ExpiresAt.After(now) {
			delete(service.challenges, id)
		}
	}
}
