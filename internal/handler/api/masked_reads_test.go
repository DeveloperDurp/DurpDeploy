package api_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestMaskedReadsIgnoreUnreadableSecrets(t *testing.T) {
	// Given: public and secret variables, with unreadable stored secrets.
	h := newAPIHarness(t)
	enableMaskingSecretBox(t, h)
	router := newMaskingRouter(h)
	project := seedProject(t, h.repo)
	admin := seedAPIUser(t, h.repo, "masked-admin@example.test", "admin")
	_, token := seedAPIToken(t, h.repo, admin.ID)
	variable, err := h.repo.Queries.CreateVariable(t.Context(),
		db.CreateVariableParams{
			ProjectID: project.ID,
			Name:      "SECRET",
			Value: sql.NullString{
				String: "enc:v1:broken",
				Valid:  true,
			},
			Secret: 1,
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.repo.CreateVariable(t.Context(), db.CreateVariableParams{
		ProjectID: project.ID, Name: "PUBLIC",
		Value: sql.NullString{String: "visible-value", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	release := seedRelease(t, h.repo, project.ID)
	if _, err := h.repo.Queries.CreateReleaseVariable(t.Context(),
		db.CreateReleaseVariableParams{
			ReleaseID: release.ID,
			Name:      "SECRET",
			Value: sql.NullString{
				String: "enc:v1:broken",
				Valid:  true,
			},
			Secret: 1,
		}); err != nil {
		t.Fatal(err)
	}
	lifecycle, err := h.repo.Queries.CreateLifecycle(t.Context(),
		db.CreateLifecycleParams{Name: "masked-lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	lifecycleVariable, err := h.repo.Queries.CreateLifecycleVariable(
		t.Context(),
		db.CreateLifecycleVariableParams{
			LifecycleID: lifecycle.ID,
			Name:        "SECRET",
			Value: sql.NullString{
				String: "enc:v1:broken",
				Valid:  true,
			},
			Secret: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.repo.Queries.SetProjectLifecycle(t.Context(),
		db.SetProjectLifecycleParams{
			ID:          project.ID,
			LifecycleID: sql.NullInt64{Int64: lifecycle.ID, Valid: true},
		}); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/projects/%d", project.ID)
	for _, path := range []string{
		base + "/variables",
		base + "/variables/inherited",
		base + "/variables?secret_only=true&limit=1&offset=0",
		fmt.Sprintf("%s/variables/%d", base, variable.ID),
		fmt.Sprintf("%s/releases/%d", base, release.ID),
		fmt.Sprintf("/api/v1/lifecycles/%d/variables", lifecycle.ID),
		fmt.Sprintf("/api/v1/lifecycles/%d/variables/%d",
			lifecycle.ID, lifecycleVariable.ID),
	} {
		t.Run(path, func(t *testing.T) {
			// When: the public API reads values whose secret values stay masked.
			response := maskingRequest(
				t,
				router,
				http.MethodGet,
				path,
				token,
				"",
			)
			// Then: an unreadable hidden value cannot fail or escape the response.
			if response.Code != http.StatusOK {
				t.Fatalf(
					"status=%d body=%s",
					response.Code,
					response.Body.String(),
				)
			}
			if strings.Contains(response.Body.String(), "enc:") ||
				strings.Contains(response.Body.String(), "broken") {
				t.Fatal("masked response exposed stored ciphertext")
			}
			if path == base+"/variables" &&
				!strings.Contains(response.Body.String(), "visible-value") {
				t.Fatal("public variable was not decrypted")
			}
			if path == base+"/variables/inherited" &&
				(!strings.Contains(response.Body.String(), "masked-lifecycle") ||
					!strings.Contains(response.Body.String(), `"override":{"id":`)) {
				t.Fatal("inherited metadata or project override was lost")
			}
		})
	}
}

func TestEnvironmentReadsSkipHiddenVerificationTarget(t *testing.T) {
	// Given: an environment with an unreadable encrypted verification target.
	h := newAPIHarness(t)
	enableMaskingSecretBox(t, h)
	router := newMaskingRouter(h)
	env, err := h.repo.Queries.CreateEnvironment(t.Context(),
		db.CreateEnvironmentParams{Name: "masked-target",
			VerificationType: "bash", VerificationTarget: "enc:v1:broken",
			VerificationTimeoutSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"viewer", "deployer", "admin"} {
		user := seedAPIUser(t, h.repo, role+"@masked.example.test", role)
		_, token := seedAPIToken(t, h.repo, user.ID)
		for _, path := range []string{"/api/v1/environments",
			fmt.Sprintf("/api/v1/environments/%d", env.ID)} {
			// When: each role reads the environment through the public API.
			response := maskingRequest(
				t,
				router,
				http.MethodGet,
				path,
				token,
				"",
			)
			// Then: only the admin, whose response needs the value, sees failure.
			want := http.StatusOK
			if role == "admin" {
				want = http.StatusInternalServerError
			}
			if response.Code != want {
				t.Errorf(
					"%s %s: status=%d, want %d",
					role,
					path,
					response.Code,
					want,
				)
			}
			if strings.Contains(response.Body.String(), "enc:") ||
				(role != "admin" && strings.Contains(response.Body.String(), "verification_target")) {
				t.Fatal("hidden verification target was returned")
			}
		}
	}
}
