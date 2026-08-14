package mfa

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-webauthn/webauthn/webauthn"
)

func (s *ChallengeService) IssueWebAuthn(
	ctx context.Context,
	issue ChallengeIssue,
	session webauthn.SessionData,
) (PendingChallenge, error) {
	ceremonyJSON, err := json.Marshal(session)
	if err != nil {
		return PendingChallenge{}, fmt.Errorf(
			"encode WebAuthn session data: %w",
			err,
		)
	}
	issue.CeremonyJSON = string(ceremonyJSON)
	return s.Issue(ctx, issue)
}
