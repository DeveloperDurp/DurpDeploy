package handler_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestVariablesPreview_MasksSecretValueOnProjectDetail(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	proj := h.makeProject("secret-preview")
	const plainValue = "visible-project-detail-value"
	const secretValue = "project-detail-secret-sentinel"
	makeVariableGlobal(t, h, proj.ID, "PLAIN_PREVIEW", plainValue, "")
	_, err := h.repo.CreateVariable(
		context.Background(),
		db.CreateVariableParams{
			ProjectID: proj.ID,
			Name:      "SECRET_PREVIEW",
			Value: sql.NullString{
				String: secretValue,
				Valid:  true,
			},
			Secret: 1,
		},
	)
	if err != nil {
		t.Fatalf("create secret preview variable: %v", err)
	}

	// When
	page := h.getProjectPage(proj.ID)

	// Then
	if strings.Contains(page, secretValue) {
		t.Errorf("project detail exposes secret preview value")
	}
	if !strings.Contains(page, plainValue) {
		t.Errorf("project detail omits non-secret preview value")
	}
	if !strings.Contains(page, "••••••••") {
		t.Errorf("project detail omits secret preview mask")
	}
}
