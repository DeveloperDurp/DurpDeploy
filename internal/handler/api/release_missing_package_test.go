package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
)

func TestReleaseMissingPackageOverride(t *testing.T) {
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "missing-package@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	upstream := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(upstream.Close)
	h.repo.ArtifactClient = &artifact.Client{HTTP: upstream.Client()}
	if _, err := h.repo.SaveProjectPackageRepository(t.Context(), project.ID,
		artifact.Repository{
			URLTemplate: upstream.URL + "/{version}.zip",
			AuthType:    "noauth",
		}); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/projects/%d/releases", project.ID)
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	req := httptest.NewRequest("POST", path,
		strings.NewReader(`{"version":"2.0.0","allow_missing_package":true}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("override=%d: %s", response.Code, response.Body.String())
	}
	var release struct {
		ID             int64 `json:"id"`
		PackageOmitted int64 `json:"package_omitted"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &release); err != nil {
		t.Fatal(err)
	}
	if release.ID == 0 || release.PackageOmitted != 1 {
		t.Fatalf("missing persisted omission state: %s", response.Body.String())
	}
}
