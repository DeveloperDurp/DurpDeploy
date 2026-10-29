//go:build e2e && agentstaging

package agentserver_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/artifact"
)

type packageSourceConfig struct {
	base, scenario string
	onFetch        func()
}

func (s *stagingE2E) packageSource(
	t *testing.T,
	config packageSourceConfig,
) string {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.Create("package.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("package")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	upstream := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(
				"Authorization",
			) != "Bearer upstream-secret" {
				w.WriteHeader(401)
				return
			}
			count := requests.Add(1)
			if config.onFetch != nil {
				config.onFetch()
			}
			if count > 1 && config.scenario == "slow-package" {
				time.Sleep(4 * time.Second)
			}
			data := archive.Bytes()
			if count > 1 && config.scenario == "changed-package" {
				data = []byte("republished")
			}
			if _, err := w.Write(data); err != nil {
				t.Error(err)
			}
		}),
	)
	t.Cleanup(upstream.Close)
	workspace, err := artifact.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := workspace.Close(); err != nil {
			t.Error(err)
		}
	})
	s.f.repo.ArtifactClient = &artifact.Client{
		HTTP:    upstream.Client(),
		TempDir: workspace.Directory,
	}
	s.api(t, "PUT", config.base+"/package-repository", map[string]string{
		"url_template": upstream.URL + "/{version}.zip",
		"auth_type":    "bearer", "credential": "upstream-secret",
	}, 200)
	return workspace.Directory
}

func (s *stagingE2E) packageProject(
	t *testing.T,
	scenario string,
) (string, int64) {
	t.Helper()
	var project, environment struct{ ID int64 }
	for path, target := range map[string]*struct{ ID int64 }{
		"/projects": &project, "/environments": &environment,
	} {
		if err := json.Unmarshal(s.api(t, "POST", path, map[string]string{
			"name": "package-" + scenario,
		}, 201), target); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"test-agent", "second-agent"} {
		s.api(t, "POST", "/admin/agents/"+id+"/environments", map[string]int64{
			"environment_id": environment.ID,
		}, 204)
	}
	return fmt.Sprintf("/projects/%d", project.ID), environment.ID
}
