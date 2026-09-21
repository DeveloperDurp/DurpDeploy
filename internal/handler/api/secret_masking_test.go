package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"
)

// newMaskingRouter builds the real server router used by the secret
// masking tests so middleware (auth, project access) matches prod.
func newMaskingRouter(h *harness) http.Handler {
	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	return server.NewRouter(
		h.repo,
		h.runner,
		parser,
		handler.NewAuthHandler(h.repo),
	)
}

// enableMaskingSecretBox turns on AES-GCM at-rest encryption so
// double-encryption regressions (ciphertext stored and re-encrypted)
// surface as failed round-trips.
func enableMaskingSecretBox(t *testing.T, h *harness) {
	t.Helper()
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	h.repo.SetSecretBox(box)
}

func maskingRequest(
	t *testing.T,
	r http.Handler,
	method, path, token, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func seedMaskingMember(
	t *testing.T,
	h *harness,
	projectID int64,
	email, role string,
) string {
	t.Helper()
	u := seedAPIUser(t, h.repo, email, role)
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: projectID,
			UserID:    u.ID,
			Role:      "admin",
		},
	); err != nil {
		t.Fatal(err)
	}
	_, token := seedAPIToken(t, h.repo, u.ID)
	return token
}

func TestVariable_SecretValuesNeverReturned(t *testing.T) {
	h := newAPIHarness(t)
	enableMaskingSecretBox(t, h)
	r := newMaskingRouter(h)
	admin := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, admin.ID)
	p := seedProject(t, h.repo)
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: p.ID,
			UserID:    admin.ID,
			Role:      "admin",
		},
	); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/projects/%d/variables", p.ID)

	// Create response must not echo the supplied secret.
	rec := maskingRequest(t, r, http.MethodPost, base, token,
		`{"name":"S","value":"super-secret","secret":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create secret: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "super-secret") {
		t.Fatalf("create echoed secret: %s", rec.Body.String())
	}

	// Non-secret variables stay readable.
	rec = maskingRequest(t, r, http.MethodPost, base, token,
		`{"name":"N","value":"plain-value","secret":false}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create plain: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "plain-value") {
		t.Fatalf("non-secret value missing: %s", rec.Body.String())
	}

	var secretID, plainID int64
	rec = maskingRequest(t, r, http.MethodGet, base, token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "super-secret") {
		t.Fatalf("list leaked secret: %s", rec.Body.String())
	}
	items := decodeListBody(t, rec.Body.Bytes())
	for _, item := range items {
		id, _ := item["id"].(float64)
		name, _ := item["name"].(string)
		if name == "S" {
			secretID = int64(id)
			if item["value"] != "" {
				t.Fatalf("list secret value = %v, want empty", item["value"])
			}
		}
		if name == "N" {
			plainID = int64(id)
			if item["value"] != "plain-value" {
				t.Fatalf("list plain value = %v", item["value"])
			}
		}
	}
	if secretID == 0 || plainID == 0 {
		t.Fatalf("seeded variables missing from list: %s", rec.Body.String())
	}

	// Single-record get follows the same policy.
	rec = maskingRequest(t, r, http.MethodGet,
		fmt.Sprintf("%s/%d", base, secretID), token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get secret: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "super-secret") {
		t.Fatalf("get leaked secret: %s", rec.Body.String())
	}

	// Update response must not echo the rotated secret.
	rec = maskingRequest(t, r, http.MethodPut,
		fmt.Sprintf("%s/%d", base, secretID), token,
		`{"name":"S","value":"rotated-secret","secret":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update secret: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "rotated-secret") {
		t.Fatalf("update echoed secret: %s", rec.Body.String())
	}

	// Metadata-only update (masked read-back sent unchanged) keeps
	// the stored credential instead of clearing it.
	rec = maskingRequest(t, r, http.MethodPut,
		fmt.Sprintf("%s/%d", base, secretID), token,
		`{"name":"S2","value":"","secret":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("metadata update: %d %s", rec.Code, rec.Body.String())
	}
	v, err := h.repo.GetVariable(context.Background(), secretID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "S2" {
		t.Fatalf("metadata update did not rename: %q", v.Name)
	}
	if v.Value.String != "rotated-secret" {
		t.Fatalf(
			"metadata update clobbered secret: %q",
			v.Value.String,
		)
	}

	// The stored value must survive for the runner despite masking.
	if v.Value.String != "rotated-secret" {
		t.Fatalf("stored value = %q, want rotated-secret", v.Value.String)
	}
}

func TestVariable_SecretMaskedByRole(t *testing.T) {
	h := newAPIHarness(t)
	r := newMaskingRouter(h)
	admin := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: p.ID,
			UserID:    admin.ID,
			Role:      "admin",
		},
	); err != nil {
		t.Fatal(err)
	}
	secretVar, err := h.repo.CreateVariable(
		context.Background(),
		db.CreateVariableParams{
			ProjectID: p.ID,
			Name:      "ROLE_SECRET",
			Value:     sql.NullString{String: "role-secret-value", Valid: true},
			Secret:    1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		role  string
		email string
	}{
		{"viewer", "viewer@example.com"},
		{"deployer", "deployer@example.com"},
		{"admin", "admin2@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			token := seedMaskingMember(t, h, p.ID, tc.email, tc.role)
			for _, path := range []string{
				fmt.Sprintf("/api/v1/projects/%d/variables", p.ID),
				fmt.Sprintf("/api/v1/projects/%d/variables/%d", p.ID, secretVar.ID),
			} {
				rec := maskingRequest(t, r, http.MethodGet, path, token, "")
				if rec.Code != http.StatusOK {
					t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), "role-secret-value") {
					t.Fatalf("GET %s leaked secret: %s", path, rec.Body.String())
				}
			}
		})
	}
}

func TestReleaseVariables_SecretValuesMasked(t *testing.T) {
	h := newAPIHarness(t)
	r := newMaskingRouter(h)
	admin := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, admin.ID)
	p := seedProject(t, h.repo)
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: p.ID,
			UserID:    admin.ID,
			Role:      "admin",
		},
	); err != nil {
		t.Fatal(err)
	}
	rel := seedRelease(t, h.repo, p.ID)
	env := seedEnv(t, h.repo)
	for _, rv := range []db.CreateReleaseVariableParams{
		{
			ReleaseID:     rel.ID,
			Name:          "REL_SECRET",
			Value:         sql.NullString{String: "release-secret", Valid: true},
			EnvironmentID: sql.NullInt64{Int64: env.ID, Valid: true},
			Secret:        1,
		},
		{
			ReleaseID:     rel.ID,
			Name:          "REL_PLAIN",
			Value:         sql.NullString{String: "release-plain", Valid: true},
			EnvironmentID: sql.NullInt64{Int64: env.ID, Valid: true},
			Secret:        0,
		},
		{
			// Valueless secret: Valid=false must not emit null.
			ReleaseID:     rel.ID,
			Name:          "REL_SECRET_EMPTY",
			Value:         sql.NullString{},
			EnvironmentID: sql.NullInt64{Int64: env.ID, Valid: true},
			Secret:        1,
		},
	} {
		if _, err := h.repo.Queries.CreateReleaseVariable(
			context.Background(), rv,
		); err != nil {
			t.Fatal(err)
		}
	}

	rec := maskingRequest(t, r, http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%d/releases/%d", p.ID, rel.ID),
		token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get release: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "release-secret") {
		t.Fatalf("release leaked secret: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "release-plain") {
		t.Fatalf("release plain value missing: %s", rec.Body.String())
	}
	var body struct {
		Variables []struct {
			Name  string  `json:"name"`
			Value *string `json:"value"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode release: %v", err)
	}
	for _, rv := range body.Variables {
		if rv.Name == "REL_SECRET" {
			if rv.Value == nil || *rv.Value != "" {
				t.Fatalf("REL_SECRET snapshot value = %v, want \"\"", rv.Value)
			}
		}
		if rv.Name == "REL_SECRET_EMPTY" {
			if rv.Value == nil || *rv.Value != "" {
				t.Fatalf("REL_SECRET_EMPTY snapshot value = %v, want \"\"", rv.Value)
			}
		}
		if rv.Name == "REL_PLAIN" {
			if rv.Value == nil || *rv.Value != "release-plain" {
				t.Fatalf("REL_PLAIN snapshot value = %v, want release-plain", rv.Value)
			}
		}
	}
}
