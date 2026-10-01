package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func TestArtifactAPIEnforcesProjectAndViewerBoundaries(t *testing.T) {
	// Given
	h := newAPIHarness(t, fakePodman(t))
	admin := seedAPIUser(t, h.repo, "artifact-admin@example.com", "admin")
	_, adminToken := seedAPIToken(t, h.repo, admin.ID)
	viewer := seedAPIUser(t, h.repo, "artifact-viewer@example.com", "viewer")
	_, viewerToken := seedAPIToken(t, h.repo, viewer.ID)
	first, second := seedProject(t, h.repo), seedProject(t, h.repo)
	source, err := h.repo.CreatePackageRepository(
		t.Context(),
		db.CreatePackageRepositoryParams{
			ProjectID:   first.ID,
			Name:        "packages",
			UrlTemplate: "https://repo.example/{version}.zip",
			AuthType:    "noauth",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	router := newMaskingRouter(h)
	base := fmt.Sprintf("/api/v1/projects/%d", second.ID)
	for _, test := range []struct {
		method, path, body, token string
		status                    int
	}{
		{"GET", fmt.Sprintf("%s/package-repositories/%d", base, source.ID), "", adminToken, 404},
		{"PUT", fmt.Sprintf("%s/package-repositories/%d", base, source.ID), `{"name":"changed","url_template":"https://repo.example/{version}.zip","auth_type":"noauth"}`, adminToken, 404},
		{"DELETE", fmt.Sprintf("%s/package-repositories/%d", base, source.ID), "", adminToken, 404},
		{"PUT", base + "/artifact-repository", fmt.Sprintf(`{"repository_id":%d}`, source.ID), adminToken, 404},
		{"POST", base + "/package-repositories", `{"name":"blocked"}`, viewerToken, 403},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			request := httptest.NewRequest(
				test.method,
				test.path,
				strings.NewReader(test.body),
			)
			request.Header.Set("Authorization", "Bearer "+test.token)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			// When
			router.ServeHTTP(response, request)
			// Then
			if response.Code != test.status {
				t.Fatalf(
					"status=%d body=%s",
					response.Code,
					response.Body.String(),
				)
			}
		})
	}
}

func TestArtifactWebViewerCannotSeeRepositoryForm(t *testing.T) {
	// Given
	h := newAPIHarness(t, fakePodman(t))
	viewer := seedAPIUser(
		t,
		h.repo,
		"artifact-form-viewer@example.com",
		"viewer",
	)
	project := seedProject(t, h.repo)
	if err := h.repo.Queries.AddProjectMember(
		t.Context(),
		db.AddProjectMemberParams{
			ProjectID: project.ID,
			UserID:    viewer.ID,
			Role:      "deployer",
		},
	); err != nil {
		t.Fatal(err)
	}
	session, csrf, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.repo.Queries.CreateSession(
		t.Context(),
		db.CreateSessionParams{
			ID:        session,
			UserID:    viewer.ID,
			CsrfToken: csrf,
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		},
	); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		"GET",
		fmt.Sprintf("/projects/%d/package-repositories/new", project.ID),
		nil,
	)
	request.AddCookie(&http.Cookie{Name: "session", Value: session})
	response := httptest.NewRecorder()
	// When
	newMaskingRouter(h).ServeHTTP(response, request)
	// Then
	if response.Code != 200 ||
		strings.Contains(response.Body.String(), `name="credential"`) ||
		strings.Contains(response.Body.String(), `name="url_template"`) {
		t.Fatalf("viewer form exposed: status=%d", response.Code)
	}
}

func TestArtifactAPIRemoteDeploymentReturnsValidationError(t *testing.T) {
	// Given
	h := newAPIHarness(t, fakePodman(t))
	admin := seedAPIUser(
		t,
		h.repo,
		"artifact-remote-admin@example.com",
		"admin",
	)
	_, token := seedAPIToken(t, h.repo, admin.ID)
	project, environment := seedProject(t, h.repo), seedEnv(t, h.repo)
	source, err := h.repo.CreatePackageRepository(
		t.Context(),
		db.CreatePackageRepositoryParams{
			ProjectID:   project.ID,
			Name:        "packages",
			UrlTemplate: "https://repo.example/{version}.zip",
			AuthType:    "noauth",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := h.repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "1",
			StepsJson: `[{"name":"remote","script_body":"true","execution_target":"agent"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.repo.Queries.CreateReleaseArtifact(
		t.Context(),
		db.CreateReleaseArtifactParams{
			ReleaseID:    release.ID,
			RepositoryID: source.ID,
			Url:          "https://repo.example/1.zip",
			Version:      "1",
			Sha256:       "pinned",
			Size:         123,
		},
	); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		"POST",
		fmt.Sprintf("/api/v1/projects/%d/deployments", project.ID),
		strings.NewReader(
			fmt.Sprintf(
				`{"release_id":%d,"environment_id":%d}`,
				release.ID,
				environment.ID,
			),
		),
	)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	// When
	newMaskingRouter(h).ServeHTTP(response, request)
	// Then
	if response.Code != 422 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
