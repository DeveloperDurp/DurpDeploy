package handler_test

import (
	"net/http"
	"sync"
	"testing"
)

func TestOIDCCallback_rejectsReplayAfterIssuingOneSessionAndAudit(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	fixture := configureOIDCCallback(t, h)
	flow := fixture.begin(t, h)

	// When
	first := flow.callback(t)
	second := flow.callback(t)

	// Then
	assertOIDCCallbackSuccess(t, first)
	assertOIDCCallbackFailure(t, second)
	assertOIDCStateCounts(t, h, [4]int{1, 1, 1, 0})
	if got := fixture.provider.Counters().Token; got != 1 {
		t.Fatalf("token exchange requests = %d, want 1", got)
	}
}

func TestOIDCCallback_rejectsConcurrentReplayAfterOneSuccessfulExchange(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	fixture := configureOIDCCallback(t, h)
	flow := fixture.begin(t, h)
	start := make(chan struct{})
	responses := make(chan *http.Response, 2)
	errors := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			response, err := doOIDCCallback(
				flow.callbackURL,
				flow.transactionCookie,
			)
			if err != nil {
				errors <- err
				return
			}
			responses <- response
		}()
	}

	// When
	close(start)
	group.Wait()
	close(responses)
	close(errors)

	// Then
	for err := range errors {
		t.Fatalf("call concurrent OIDC callback: %v", err)
	}
	successes := 0
	failures := 0
	for response := range responses {
		if response.Header.Get("Location") == "/" {
			assertOIDCCallbackSuccess(t, response)
			successes++
			continue
		}
		assertOIDCCallbackFailure(t, response)
		failures++
	}
	if successes != 1 || failures != 1 {
		t.Fatalf(
			"concurrent callback results = %d success, %d failure",
			successes,
			failures,
		)
	}
	assertOIDCStateCounts(t, h, [4]int{1, 1, 1, 0})
	if got := fixture.provider.Counters().Token; got != 1 {
		t.Fatalf("token exchange requests = %d, want 1", got)
	}
}
