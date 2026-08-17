package oidc

import (
	"context"
	"sync"
	"testing"
)

func TestIdentity_ResolvesOneUserWhenConcurrent(t *testing.T) {
	// Given
	repo := newIdentityRepository(t)
	identity := testIdentity()
	start := make(chan struct{})
	results := make(chan identityResult, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			user, err := ResolveIdentity(context.Background(), repo, identity)
			results <- identityResult{userID: user.ID, err: err}
		})
	}

	// When
	close(start)
	workers.Wait()
	close(results)

	// Then
	var userID int64
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent resolve identity: %v", result.err)
		}
		if userID == 0 {
			userID = result.userID
		}
		if result.userID != userID {
			t.Fatalf("resolved user ID = %d, want %d", result.userID, userID)
		}
	}
	if userID == 0 || countOIDCIdentities(t, repo) != 1 {
		t.Fatalf(
			"concurrent resolution user ID = %d, identities = %d",
			userID,
			countOIDCIdentities(t, repo),
		)
	}
}

type identityResult struct {
	userID int64
	err    error
}
