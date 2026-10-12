//go:build e2e

package api_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestVariableOwnershipBeforeDecryptionAPIWebE2E(t *testing.T) {
	// Given: accessible project URLs and valid or corrupt foreign ciphertext.
	f := newArtifactE2E(t)
	foreign, err := f.h.repo.Queries.CreateProject(t.Context(),
		db.CreateProjectParams{Name: "foreign-variable-owner"})
	if err != nil {
		t.Fatal(err)
	}
	valid, err := f.h.repo.CreateVariable(t.Context(), db.CreateVariableParams{
		ProjectID: foreign.ID, Name: "FOREIGN_VALID", Secret: 1,
		Value: sql.NullString{String: "ownership-secret-sentinel", Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	corrupt, err := f.h.repo.Queries.CreateVariable(t.Context(),
		db.CreateVariableParams{
			ProjectID: foreign.ID, Name: "FOREIGN_CORRUPT", Secret: 1,
			Value: sql.NullString{String: "invalid-ciphertext!", Valid: true},
		})
	if err != nil {
		t.Fatal(err)
	}
	var apiMissing, webMissing string
	for _, fixture := range []struct {
		name string
		id   int64
	}{
		{"missing", corrupt.ID + 1000},
		{"foreign valid", valid.ID},
		{"foreign corrupt", corrupt.ID},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			path := fmt.Sprintf("/projects/%d/variables/%d",
				f.project.ID, fixture.id)
			// When: API reads or web edit/update targets another project's ID.
			apiBody := string(f.api(t, "GET", "/api/v1"+path, nil, 404))
			webBody := f.web(t, "GET", path+"/edit", nil, 404)
			f.web(t, "PUT", path, nil, 404)
			// Then: all variants have the same not-found outcome without decrypting.
			if fixture.name == "missing" {
				apiMissing, webMissing = apiBody, webBody
			} else if apiBody != apiMissing || webBody != webMissing {
				t.Fatal("foreign variable existence changed the not-found response")
			}
			if strings.Contains(apiBody+webBody, "ownership-secret-sentinel") ||
				strings.Contains(apiBody+webBody, "invalid-ciphertext!") {
				t.Fatal("variable contents leaked through the rejection")
			}
		})
	}
	// A matching owner still reaches decryption, so corrupt storage is not hidden.
	f.api(t, "GET", fmt.Sprintf("/api/v1/projects/%d/variables/%d",
		foreign.ID, corrupt.ID), nil, 500)
}
