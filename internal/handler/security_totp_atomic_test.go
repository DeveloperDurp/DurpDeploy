package handler_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestSecurity_TOTPEnrollmentRollsBackWhenRecoveryPersistenceFails(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	staged := stageTOTP(t, h, current)
	if _, err := h.repo.DB.ExecContext(context.Background(), `
		CREATE TRIGGER reject_recovery_code
		BEFORE INSERT ON mfa_recovery_codes
		BEGIN
			SELECT RAISE(ABORT, 'test recovery insert failure');
		END`,
	); err != nil {
		t.Fatalf("create recovery failure trigger: %v", err)
	}

	// When
	response := postSecurityValues(
		t,
		current,
		h.server,
		"/settings/security/totp/confirm",
		enrollmentValues(staged, totpCode(t, staged.seed, time.Now())),
	)
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusUnprocessableEntity ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM mfa_totp WHERE user_id = ?",
			current.user.ID,
		) != 0 || countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = ?",
		current.user.ID,
	) != 0 || countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
		current.user.ID,
	) != 1 {
		t.Fatal(
			"failed enrollment did not roll back factor, recovery codes, and challenge consumption",
		)
	}
}

func TestSecurity_TOTPEnrollmentPreservesRecoveryCodesWhenAddingFactor(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		securityCredentialParams(current.user.ID, []byte{1}),
	); err != nil {
		t.Fatalf("create existing factor: %v", err)
	}
	hash, err := mfa.HashRecoveryCode("0123-4567-89AB-CDEF-0123-4567-89AB-CDEF")
	if err != nil {
		t.Fatalf("hash existing recovery code: %v", err)
	}
	if _, err := h.repo.Queries.CreateRecoveryCode(
		context.Background(),
		db.CreateRecoveryCodeParams{
			ID:       "existing-recovery",
			UserID:   current.user.ID,
			CodeHash: hash[:],
		},
	); err != nil {
		t.Fatalf("create existing recovery code: %v", err)
	}
	staged := stageTOTP(t, h, current)

	// When
	response := postSecurityValues(
		t,
		current,
		h.server,
		"/settings/security/totp/confirm",
		enrollmentValues(staged, totpCode(t, staged.seed, time.Now())),
	)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read enrollment response: %v", err)
	}

	// Then
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/login" ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM mfa_totp WHERE user_id = ?",
			current.user.ID,
		) != 1 || countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = ?",
		current.user.ID,
	) != 1 || strings.Contains(string(body), "recovery-code") {
		t.Fatal("adding TOTP replaced or disclosed existing recovery codes")
	}
}
