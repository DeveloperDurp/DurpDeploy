//go:build e2e

package api_test

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"github.com/google/uuid"
)

func foreignKeyFixture(t *testing.T) *artifactE2E {
	t.Helper()
	base := strings.TrimRight(os.Getenv("DURPDEPLOY_LIVE_BASE"), "/")
	if base == "" {
		return newArtifactE2E(t)
	}
	return &artifactE2E{
		baseURL: base,
		token:   os.Getenv("DURPDEPLOY_LIVE_API_TOKEN"),
		session: os.Getenv("DURPDEPLOY_LIVE_SESSION"),
		csrf:    os.Getenv("DURPDEPLOY_LIVE_CSRF"),
		client: &http.Client{
			Timeout:       time.Minute,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func foreignKeyReferences(
	t *testing.T,
	f *artifactE2E,
) (db.Environment, int64) {
	t.Helper()
	var environment db.Environment
	decodeStepLogTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/environments",
			map[string]string{"name": "FK reference " + uuid.NewString()},
			201,
		),
		&environment,
	)
	if path := os.Getenv("DURPDEPLOY_FOREIGN_KEY_ENV_RECORD"); path != "" {
		record, err := os.OpenFile(
			path,
			os.O_CREATE|os.O_APPEND|os.O_WRONLY,
			0600,
		)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := fmt.Fprintln(record, environment.ID)
		closeErr := record.Close()
		if writeErr != nil {
			t.Fatal(writeErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	var lifecycle struct{ ID int64 }
	decodeStepLogTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/lifecycles",
			map[string]string{"name": "FK safeguard " + uuid.NewString()},
			201,
		),
		&lifecycle,
	)
	var stage struct{ ID int64 }
	decodeStepLogTest(
		t,
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID),
			map[string]int64{"environment_id": environment.ID, "sort_order": 1},
			201,
		),
		&stage,
	)
	t.Logf(
		"Retained lifecycle: %s/lifecycles/%d; environment: %s/environments/%d/edit",
		f.baseURL,
		lifecycle.ID,
		f.baseURL,
		environment.ID,
	)
	return environment, lifecycle.ID
}

func TestForeignKeyValidationAPIWebE2E(t *testing.T) {
	f := foreignKeyFixture(t)
	environment, lifecycle := foreignKeyReferences(t, f)
	path := fmt.Sprintf("/environments/%d", environment.ID)
	result := f.api(t, "DELETE", "/api/v1"+path, nil, 409)
	if !strings.Contains(string(result), "1 lifecycle stages") {
		t.Fatalf("missing reference detail: %s", result)
	}
	if body := f.web(
		t,
		"DELETE",
		path,
		nil,
		409,
	); !strings.Contains(
		body,
		"lifecycle stages",
	) {
		t.Fatalf("web error: %s", body)
	}
	f.api(t, "GET", "/api/v1"+path, nil, 200)
	// Remove the lifecycle through its public API, then prove normal deletion.
	f.api(
		t,
		"DELETE",
		fmt.Sprintf("/api/v1/lifecycles/%d", lifecycle),
		nil,
		204,
	)
	f.api(t, "DELETE", "/api/v1"+path, nil, 204)
	// Recreate a blocked example for manual checks on running servers.
	foreignKeyReferences(t, f)
	var project struct{ ID int64 }
	decodeStepLogTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/projects",
			map[string]string{"name": "FK variables " + uuid.NewString()},
			201,
		),
		&project,
	)
	variables := fmt.Sprintf("/projects/%d/variables", project.ID)
	const missing = int64(2147483647)
	f.api(
		t,
		"POST",
		"/api/v1"+variables,
		map[string]any{"name": "MISSING", "environment_id": missing},
		422,
	)
	f.web(
		t,
		"POST",
		variables,
		url.Values{
			"name":           {"MISSING"},
			"environment_id": {fmt.Sprint(missing)},
		},
		422,
	)
	var variable struct{ ID int64 }
	decodeStepLogTest(t, f.api(
		t,
		"POST",
		"/api/v1"+variables,
		map[string]string{"name": "VALID", "value": "kept"},
		201,
	), &variable)
	f.api(
		t,
		"PUT",
		fmt.Sprintf("/api/v1%s/%d", variables, variable.ID),
		map[string]any{"name": "VALID", "environment_id": missing},
		422,
	)
	var release db.Release
	decodeStepLogTest(
		t,
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/projects/%d/releases", project.ID),
			map[string]string{"version": "v1"},
			201,
		),
		&release,
	)
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/projects/%d/deployments", project.ID),
		map[string]int64{"release_id": release.ID, "environment_id": missing},
		404,
	)
	f.web(
		t,
		"POST",
		fmt.Sprintf("/projects/%d/deploy", project.ID),
		url.Values{
			"release_id":     {fmt.Sprint(release.ID)},
			"environment_id": {fmt.Sprint(missing)},
		},
		422,
	)
	t.Logf(
		"Retained variable/deployment validation: %s/projects/%d",
		f.baseURL,
		project.ID,
	)
}

func TestForeignKeyAdmissionRaceAPIWebE2E(t *testing.T) {
	// Use an isolated database to inject a parent lost after HTTP validation.
	f := newArtifactE2E(t)
	release := verificationRelease(t, f, "lost-parent")
	if _, err := f.h.repo.DB.Exec(`CREATE TRIGGER disappear_at_release_lock
BEFORE UPDATE OF version ON releases BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/projects/%d", f.project.ID)
	body := f.api(t, "POST", "/api/v1"+path+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": f.environment.ID,
	}, http.StatusConflict)
	if !strings.Contains(string(body), "release or environment") {
		t.Fatalf("missing parent detail: %s", body)
	}
	f.web(t, "POST", path+"/deploy", url.Values{
		"release_id":     {fmt.Sprint(release.ID)},
		"environment_id": {fmt.Sprint(f.environment.ID)},
	}, http.StatusConflict)
	var count int
	if err := f.h.repo.DB.QueryRow("SELECT COUNT(*) FROM deployments").
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed admission created %d deployments", count)
	}
	if _, err := f.h.repo.DB.Exec(
		"DROP TRIGGER disappear_at_release_lock",
	); err != nil {
		t.Fatal(err)
	}
	f.api(t, "POST", "/api/v1"+path+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": f.environment.ID,
	}, http.StatusCreated)
}
