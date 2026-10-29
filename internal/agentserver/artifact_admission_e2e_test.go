//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeploymentPackageAdmissionAPIE2E(t *testing.T) {
	for _, budget := range []string{"global", "per-agent"} {
		t.Run(budget, func(t *testing.T) {
			checkDeploymentPackageAdmission(t, budget)
		})
	}
}

func checkDeploymentPackageAdmission(t *testing.T, budget string) {
	t.Helper()
	var repo *repository.Repository
	var once sync.Once
	var mu sync.Mutex
	var permits []func()
	release := func() {
		mu.Lock()
		defer mu.Unlock()
		for _, finish := range permits {
			finish()
		}
		permits = nil
	}
	var fetches atomic.Int64
	var workspace string
	blocked := make(chan struct{}, 1)
	action := "download"
	if budget == "per-agent" {
		action = "upload"
	}
	s := newStagingE2EWithTransfers(
		t,
		"",
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					if !strings.HasSuffix(r.URL.Path, "/artifact") {
						next.ServeHTTP(w, r)
						return
					}
					once.Do(func() {
						mu.Lock()
						defer mu.Unlock()
						permits = occupyStagingAdmissionPermits(t, repo, action)
					})
					before := fetches.Load()
					response := &stagingAdmissionResponse{ResponseWriter: w}
					next.ServeHTTP(response, r)
					if response.status == 429 {
						assert.Equal(
							t,
							"1",
							response.Header().Get("Retry-After"),
						)
						assert.Equal(
							t,
							before,
							fetches.Load(),
							"rejected request fetched package",
						)
						files, err := os.ReadDir(workspace)
						if assert.NoError(t, err) && assert.Len(t, files, 1) {
							assert.Equal(t, ".lock", files[0].Name())
						}
						t.Logf(
							"%s %s => 429, Retry-After=%q; budget=%s, upstream requests=%d, workspace entries=%d",
							r.Method,
							r.URL.Path,
							response.Header().Get("Retry-After"),
							budget,
							before,
							len(files),
						)
						select {
						case blocked <- struct{}{}:
						default:
						}
					}
				},
			)
		},
	)
	repo = s.f.repo
	t.Cleanup(release)
	base, environmentID := s.packageProject(t, "admission-"+budget)
	workspace = s.packageSource(t, packageSourceConfig{
		base: base, onFetch: func() { fetches.Add(1) },
	})
	s.api(t, "POST", base+"/steps", map[string]any{
		"name": "package", "interpreter": "bash", "execution_target": "agent",
		"agent_execution_mode": "host", "agent_selectors": []string{"test-agent"},
		"script_body":     `test "$(cat "$ARTIFACT_PATH/package.txt")" = package; echo package-admission-ok`,
		"timeout_seconds": 30,
	}, 201)
	var releaseRecord, deployment struct{ ID int64 }
	require.NoError(t, json.Unmarshal(s.api(t, "POST", base+"/releases",
		map[string]string{"version": "v1"}, 201), &releaseRecord))
	require.NoError(t, json.Unmarshal(s.api(
		t,
		"POST",
		base+"/deployments",
		map[string]int64{
			"release_id":     releaseRecord.ID,
			"environment_id": environmentID,
		},
		201,
	), &deployment))
	select {
	case <-blocked:
	case <-time.After(30 * time.Second):
		t.Fatal("agent did not receive package overload response")
	}
	release()
	s.wait(t, deployment.ID, "succeeded")
	path := fmt.Sprintf("/deployments/%d", deployment.ID)
	logs := string(s.api(t, "GET", path+"/logs.txt", nil, 200))
	require.Contains(t, logs, "package-admission-ok")
	require.NotContains(t, logs, "upstream-secret")
	s.api(
		t,
		"GET",
		fmt.Sprintf("%s/releases/%d/artifact", base, releaseRecord.ID),
		nil,
		200,
	)
	s.api(t, "GET", path+"/staging", nil, 200)
	s.assertTransferPurged(t, "package-admission", deployment.ID)
	s.assertRuntimeStagingPurged(t)
	files, err := os.ReadDir(workspace)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, ".lock", files[0].Name())
	t.Logf(
		"deployment %d succeeded after retry; package log marker present, staging empty, ZIPs removed",
		deployment.ID,
	)
}
