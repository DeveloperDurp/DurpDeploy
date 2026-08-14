package mfa

import (
	"context"
	"sync"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestChallenge_ConsumeWithCommitsOneCredentialCounterUpdate(
	t *testing.T,
) {
	// Given
	ctx := context.Background()
	service, binding, conn := newChallengeServiceForTest(t)
	repo := repository.New(conn)
	factors := NewFactorStore(FactorStoreConfig{Repository: repo})
	credential, err := repo.Queries.CreateWebAuthnCredential(
		ctx,
		db.CreateWebAuthnCredentialParams{
			CredentialID:   []byte("atomic-counter"),
			UserID:         binding.UserID,
			Name:           "atomic",
			PublicKey:      []byte{1},
			TransportsJson: "[]",
		},
	)
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}

	// When
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, signCount := range []int64{1, 2} {
		group.Add(1)
		go func(nextSignCount int64) {
			defer group.Done()
			<-start
			results <- service.ConsumeWith(
				ctx,
				binding,
				func(
					ctx context.Context,
					queries *db.Queries,
					_ db.MfaChallenge,
				) error {
					_, err := factors.UpdateCredentialCounterWith(
						ctx,
						queries,
						credential.CredentialID,
						nextSignCount,
						0,
					)
					return err
				},
			)
		}(signCount)
	}
	close(start)
	group.Wait()
	close(results)

	// Then
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful counter updates = %d, want 1", successes)
	}
	stored, err := repo.Queries.GetWebAuthnCredentialByID(
		ctx,
		credential.CredentialID,
	)
	if err != nil {
		t.Fatalf("load credential: %v", err)
	}
	if stored.SignCount != 1 && stored.SignCount != 2 {
		t.Fatalf(
			"stored credential sign count = %d, want winning update",
			stored.SignCount,
		)
	}
}
