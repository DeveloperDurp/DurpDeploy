package handler_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

const atomicRecoveryCode = "0123456789ABCDEF0123456789ABCDEF"

func TestLogin_TOTPDoesNotAdvanceWhenFinalSessionCreationFails(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	user := h.seedUser(t, "totp-atomic@example.com", "hunter2")
	code := seedTOTP(t, h, user, box)
	pending := loginPending(t, h, user.Email)
	blockFinalSessionCreation(t, h)

	// When
	response, err := pending.client.PostForm(
		h.server+"/login/mfa/totp",
		url.Values{"code": {code}, "csrf_token": {pending.csrf}},
	)
	if err != nil {
		t.Fatalf("post TOTP: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("TOTP response = %d, want 422", response.StatusCode)
	}
	factor, err := h.repo.Queries.GetTOTPByUserID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("load TOTP factor: %v", err)
	}
	if factor.LastAcceptedStep.Valid {
		t.Fatal("failed final session creation advanced the TOTP replay step")
	}
}

func TestLogin_RecoveryDoesNotConsumeWhenFinalSessionCreationFails(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	user := h.seedUser(t, "recovery-atomic@example.com", "hunter2")
	seedTOTP(t, h, user, box)
	seedAtomicRecoveryCode(t, h, user.ID)
	pending := loginPending(t, h, user.Email)
	blockFinalSessionCreation(t, h)

	// When
	response, err := pending.client.PostForm(
		h.server+"/login/mfa/recovery",
		url.Values{
			"code":       {atomicRecoveryCode},
			"csrf_token": {pending.csrf},
		},
	)
	if err != nil {
		t.Fatalf("post recovery code: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("recovery response = %d, want 422", response.StatusCode)
	}
	if recoveryCodeWasUsed(t, h) {
		t.Fatal("failed final session creation consumed the recovery code")
	}
}

func TestLogin_ConcurrentAlternateFactorsOnlyMutateWinningChallenge(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	user := h.seedUser(t, "alternate-factors@example.com", "hunter2")
	code := seedTOTP(t, h, user, box)
	seedAtomicRecoveryCode(t, h, user.ID)
	pending := loginPending(t, h, user.Email)
	submissions := []mfaSubmission{
		{
			path:   h.server + "/login/mfa/totp",
			values: url.Values{"code": {code}, "csrf_token": {pending.csrf}},
		},
		{
			path: h.server + "/login/mfa/recovery",
			values: url.Values{
				"code":       {atomicRecoveryCode},
				"csrf_token": {pending.csrf},
			},
		},
	}
	start := make(chan struct{})
	results := make(chan mfaSubmissionResult, len(submissions))
	var group sync.WaitGroup

	// When
	for _, submission := range submissions {
		group.Add(1)
		go func(submission mfaSubmission) {
			defer group.Done()
			<-start
			response, err := pendingReplayClient(t, h, pending).
				PostForm(submission.path, submission.values)
			results <- mfaSubmissionResult{response: response, err: err}
		}(submission)
	}
	close(start)
	group.Wait()
	close(results)

	// Then
	successes := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("post alternate MFA factor: %v", result.err)
		}
		result.response.Body.Close()
		if result.response.StatusCode == http.StatusSeeOther {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful alternate factors = %d, want 1", successes)
	}
	factor, err := h.repo.Queries.GetTOTPByUserID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("load TOTP factor: %v", err)
	}
	if factor.LastAcceptedStep.Valid && recoveryCodeWasUsed(t, h) {
		t.Fatal("both alternate factors mutated for one consumed challenge")
	}
}

type mfaSubmission struct {
	path   string
	values url.Values
}

type mfaSubmissionResult struct {
	response *http.Response
	err      error
}

func blockFinalSessionCreation(t *testing.T, h *authHarness) {
	t.Helper()
	_, err := h.repo.DB.ExecContext(context.Background(), `
		CREATE TRIGGER fail_final_session
		BEFORE INSERT ON sessions
		BEGIN
			SELECT RAISE(ABORT, 'forced final session failure');
		END`)
	if err != nil {
		t.Fatalf("block final session creation: %v", err)
	}
}

func seedAtomicRecoveryCode(t *testing.T, h *authHarness, userID int64) {
	t.Helper()
	hash, err := mfa.HashRecoveryCode(atomicRecoveryCode)
	if err != nil {
		t.Fatalf("hash recovery code: %v", err)
	}
	if _, err := h.repo.Queries.CreateRecoveryCode(
		context.Background(),
		db.CreateRecoveryCodeParams{
			ID: "atomic-recovery", UserID: userID, CodeHash: hash[:],
		},
	); err != nil {
		t.Fatalf("create recovery code: %v", err)
	}
}

func recoveryCodeWasUsed(t *testing.T, h *authHarness) bool {
	t.Helper()
	var usedAt sql.NullInt64
	if err := h.repo.DB.QueryRowContext(
		context.Background(),
		"SELECT used_at FROM mfa_recovery_codes WHERE id = 'atomic-recovery'",
	).Scan(&usedAt); err != nil {
		t.Fatalf("load recovery code: %v", err)
	}
	return usedAt.Valid
}

func pendingReplayClient(
	t *testing.T,
	h *authHarness,
	pending pendingLogin,
) *http.Client {
	t.Helper()
	serverURL, err := url.Parse(h.server)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("new replay cookie jar: %v", err)
	}
	jar.SetCookies(serverURL, []*http.Cookie{
		{Name: "mfa_pending", Value: pending.token, Path: "/"},
		{Name: "mfa_csrf", Value: pending.csrf, Path: "/"},
	})
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
