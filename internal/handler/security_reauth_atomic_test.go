package handler_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestSecurity_ReauthConsumesOneRecoveryCodeForConcurrentSubmissions(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	seedTOTP(t, h, *current.user, box)
	codes, err := mfa.GenerateRecoveryCodes(nil)
	if err != nil {
		t.Fatalf("generate recovery codes: %v", err)
	}
	for index, code := range codes[:2] {
		hash, err := mfa.HashRecoveryCode(code)
		if err != nil {
			t.Fatalf("hash recovery code: %v", err)
		}
		if _, err := h.repo.Queries.CreateRecoveryCode(
			context.Background(),
			db.CreateRecoveryCodeParams{
				ID:       fmt.Sprintf("reauth-race-%d", index),
				UserID:   current.user.ID,
				CodeHash: hash[:],
			},
		); err != nil {
			t.Fatalf("create recovery code: %v", err)
		}
	}
	password := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth",
		url.Values{"password": {"testpass"}},
		h.authHandler.SecurityReauthPost,
	)
	challenge := securityHiddenValues(t, password.Body.String())

	// When
	start := make(chan struct{})
	responses := make(chan int, 2)
	var group sync.WaitGroup
	for _, code := range codes[:2] {
		group.Add(1)
		go func(recoveryCode string) {
			defer group.Done()
			<-start
			response := postSecurityForm(
				t,
				h,
				current,
				"/settings/security/reauth/recovery",
				url.Values{
					"challenge_token": {challenge.Get("challenge_token")},
					"challenge_csrf":  {challenge.Get("challenge_csrf")},
					"code":            {recoveryCode},
				},
				h.authHandler.SecurityReauthRecoveryPost,
			)
			responses <- response.Code
		}(code)
	}
	close(start)
	group.Wait()
	close(responses)

	// Then
	successes := 0
	for status := range responses {
		if status == http.StatusSeeOther {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf(
			"successful reauthentication submissions = %d, want 1",
			successes,
		)
	}
	if unused := countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = ? AND used_at IS NULL",
		current.user.ID,
	); unused != 1 {
		t.Fatalf(
			"unused recovery codes after one challenge = %d, want 1",
			unused,
		)
	}
}

func TestSecurity_ReauthCommitsOneFactorMutationForConcurrentTOTPAndRecovery(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	totpCode := seedTOTP(t, h, *current.user, box)
	codes, err := mfa.GenerateRecoveryCodes(nil)
	if err != nil {
		t.Fatalf("generate recovery codes: %v", err)
	}
	hash, err := mfa.HashRecoveryCode(codes[0])
	if err != nil {
		t.Fatalf("hash recovery code: %v", err)
	}
	if _, err := h.repo.Queries.CreateRecoveryCode(
		context.Background(),
		db.CreateRecoveryCodeParams{
			ID:       "reauth-totp-race",
			UserID:   current.user.ID,
			CodeHash: hash[:],
		},
	); err != nil {
		t.Fatalf("create recovery code: %v", err)
	}
	password := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth",
		url.Values{"password": {"testpass"}},
		h.authHandler.SecurityReauthPost,
	)
	challenge := securityHiddenValues(t, password.Body.String())

	// When
	start := make(chan struct{})
	responses := make(chan int, 2)
	var group sync.WaitGroup
	for _, submission := range []struct {
		path    string
		code    string
		handler http.HandlerFunc
	}{
		{
			path:    "/settings/security/reauth/totp",
			code:    totpCode,
			handler: h.authHandler.SecurityReauthTOTPPost,
		},
		{
			path:    "/settings/security/reauth/recovery",
			code:    codes[0],
			handler: h.authHandler.SecurityReauthRecoveryPost,
		},
	} {
		group.Add(1)
		go func(submission struct {
			path    string
			code    string
			handler http.HandlerFunc
		}) {
			defer group.Done()
			<-start
			response := postSecurityForm(
				t,
				h,
				current,
				submission.path,
				url.Values{
					"challenge_token": {challenge.Get("challenge_token")},
					"challenge_csrf":  {challenge.Get("challenge_csrf")},
					"code":            {submission.code},
				},
				submission.handler,
			)
			responses <- response.Code
		}(submission)
	}
	close(start)
	group.Wait()
	close(responses)

	// Then
	successes := 0
	for status := range responses {
		if status == http.StatusSeeOther {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf(
			"successful reauthentication submissions = %d, want 1",
			successes,
		)
	}
	unusedRecoveryCodes := countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = ? AND used_at IS NULL",
		current.user.ID,
	)
	var acceptedStep sql.NullInt64
	if err := h.repo.DB.QueryRowContext(
		context.Background(),
		"SELECT last_accepted_step FROM mfa_totp WHERE user_id = ?",
		current.user.ID,
	).Scan(&acceptedStep); err != nil {
		t.Fatalf("read accepted TOTP step: %v", err)
	}
	factorMutations := 0
	if unusedRecoveryCodes == 0 {
		factorMutations++
	}
	if acceptedStep.Valid {
		factorMutations++
	}
	if factorMutations != 1 {
		t.Fatalf(
			"factor mutations after one challenge = %d, want 1",
			factorMutations,
		)
	}
}
