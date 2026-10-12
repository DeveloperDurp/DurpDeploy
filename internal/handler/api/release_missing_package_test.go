package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
)

func TestReleaseDefersMissingPackagePull(t *testing.T) {
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "missing-package@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	var requests atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			http.NotFound(w, r)
		}))
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
		strings.NewReader(`{"version":"2.0.0"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("override=%d: %s", response.Code, response.Body.String())
	}
	var release struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &release); err != nil {
		t.Fatal(err)
	}
	if release.ID == 0 {
		t.Fatalf("release was not created: %s", response.Body.String())
	}
	if requests.Load() != 0 {
		t.Fatal("release creation contacted the package server")
	}
}
