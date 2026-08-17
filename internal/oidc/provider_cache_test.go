package oidc

import (
	"context"
	"sync"
	"testing"
)

func TestProviderExchangeReusesJWKSForFixedKey(t *testing.T) {
	// Given
	fixture := newProviderFixture(t)
	provider := fixture.newProvider(t)

	// When
	for _, code := range []string{fixture.authorizationCode, fixture.NewCode()} {
		if _, err := provider.Exchange(
			context.Background(),
			code,
			fixture.transaction,
		); err != nil {
			t.Fatalf("Exchange() error = %v", err)
		}
	}

	// Then
	if got := fixture.Counters().Discovery; got != 1 {
		t.Fatalf("discovery requests = %d, want 1", got)
	}
	if got := fixture.Counters().JWKS; got != 1 {
		t.Fatalf("JWKS requests = %d, want 1", got)
	}
}

func TestProviderExchangeSharesJWKSDuringConcurrentFirstUse(t *testing.T) {
	// Given
	fixture := newProviderFixture(t)
	provider := fixture.newProvider(t)
	start := make(chan struct{})
	errs := make(chan error, 8)
	var waitGroup sync.WaitGroup
	for range cap(errs) {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			_, err := provider.Exchange(
				context.Background(),
				fixture.NewCode(),
				fixture.transaction,
			)
			errs <- err
		}()
	}

	// When
	close(start)
	waitGroup.Wait()
	close(errs)

	// Then
	for err := range errs {
		if err != nil {
			t.Fatal("concurrent Exchange() returned an error")
		}
	}
	if got := fixture.Counters().Discovery; got != 1 {
		t.Fatalf("discovery requests = %d, want 1", got)
	}
	if got := fixture.Counters().JWKS; got != 1 {
		t.Fatalf("JWKS requests = %d, want 1", got)
	}
}

func TestProviderExchangeRefreshesJWKSAfterKeyRotation(t *testing.T) {
	// Given
	fixture := newProviderFixture(t)
	provider := fixture.newProvider(t)
	if _, err := provider.Exchange(
		context.Background(),
		fixture.NewCode(),
		fixture.transaction,
	); err != nil {
		t.Fatalf("first Exchange() error = %v", err)
	}
	fixture.rotateSigningKey(t)

	// When
	if _, err := provider.Exchange(
		context.Background(),
		fixture.NewCode(),
		fixture.transaction,
	); err != nil {
		t.Fatalf("rotated-key Exchange() error = %v", err)
	}

	// Then
	if got := fixture.Counters().Discovery; got != 1 {
		t.Fatalf("discovery requests = %d, want 1", got)
	}
	if got := fixture.Counters().JWKS; got != 2 {
		t.Fatalf("JWKS requests = %d, want 2 after rotation", got)
	}
}
