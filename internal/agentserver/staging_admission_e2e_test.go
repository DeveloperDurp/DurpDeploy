//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type stagingAdmissionResponse struct {
	http.ResponseWriter
	status int
}

func (w *stagingAdmissionResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *stagingAdmissionResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func TestDeploymentStagingAdmissionAPIE2E(t *testing.T) {
	for _, action := range []string{"download", "upload"} {
		t.Run(action, func(t *testing.T) {
			testDeploymentStagingAdmission(t, action)
		})
	}
}

func testDeploymentStagingAdmission(t *testing.T, action string) {
	t.Helper()
	var repo *repository.Repository
	var once sync.Once
	var permitMu sync.Mutex
	var permits []func()
	releasePermits := func() {
		permitMu.Lock()
		defer permitMu.Unlock()
		for _, done := range permits {
			done()
		}
		permits = nil
	}
	blocked := make(chan string, 1)
	s := newStagingE2EWithTransfers(
		t,
		"",
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					if !strings.HasSuffix(
						r.URL.Path,
						"/staging/"+action,
					) {
						next.ServeHTTP(w, r)
						return
					}
					once.Do(func() {
						permitMu.Lock()
						defer permitMu.Unlock()
						permits = occupyStagingAdmissionPermits(t, repo, action)
					})
					response := &stagingAdmissionResponse{
						ResponseWriter: w,
					}
					next.ServeHTTP(response, r)
					if response.status == 429 {
						select {
						case blocked <- response.Header().Get("Retry-After"):
						default:
						}
					}
				},
			)
		},
	)
	repo = s.f.repo
	t.Cleanup(releasePermits)
	var project, environment, release, deployment struct{ ID int64 }
	decode := func(data []byte, into any) {
		t.Helper()
		if err := json.Unmarshal(data, into); err != nil {
			t.Fatal(err)
		}
	}
	decode(
		s.api(
			t,
			"POST",
			"/projects",
			map[string]string{"name": "admission-" + action},
			201,
		),
		&project,
	)
	decode(
		s.api(
			t,
			"POST",
			"/environments",
			map[string]string{"name": "admission-" + action},
			201,
		),
		&environment,
	)
	s.api(
		t,
		"POST",
		"/admin/agents/test-agent/environments",
		map[string]int64{"environment_id": environment.ID},
		204,
	)
	base := fmt.Sprintf("/projects/%d", project.ID)
	s.api(t, "POST", base+"/steps", map[string]any{
		"name": "admission", "script_body": "echo admission-ok", "interpreter": "bash",
		"execution_target": "agent", "agent_execution_mode": "host",
		"agent_selectors": []string{
			"test-agent",
		}, "timeout_seconds": 30,
	}, 201)
	decode(
		s.api(
			t,
			"POST",
			base+"/releases",
			map[string]string{"version": "v1"},
			201,
		),
		&release,
	)
	decode(
		s.api(
			t,
			"POST",
			base+"/deployments",
			map[string]int64{
				"release_id":     release.ID,
				"environment_id": environment.ID,
			},
			201,
		),
		&deployment,
	)
	select {
	case retry := <-blocked:
		if retry != "1" {
			t.Fatalf("Retry-After=%q", retry)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("agent did not observe transfer overload")
	}
	releasePermits()
	s.wait(t, deployment.ID, "succeeded")
	logs := string(
		s.api(
			t,
			"GET",
			fmt.Sprintf("/deployments/%d/logs.txt", deployment.ID),
			nil,
			200,
		),
	)
	if !strings.Contains(logs, "admission-ok") {
		t.Fatal("agent retry did not run script")
	}
	s.api(
		t,
		"GET",
		fmt.Sprintf("/deployments/%d/staging", deployment.ID),
		nil,
		200,
	)
	s.assertTransferPurged(t, action, deployment.ID)
	s.assertRuntimeStagingPurged(t)
}

func occupyStagingAdmissionPermits(
	t *testing.T,
	repo *repository.Repository,
	action string,
) []func() {
	t.Helper()
	var permits []func()
	count := 4
	if action == "upload" {
		count = 1
	}
	for i := range count {
		agent := fmt.Sprintf("occupied-%d", i)
		if action == "upload" {
			agent = "test-agent"
		}
		done, ok := repo.TryBeginStagingTransfer(
			db.GetStagingSnapshotParams{
				Producer: agent,
			},
		)
		if !ok {
			t.Error(
				"failed to occupy transfer capacity",
			)
			continue
		}
		permits = append(permits, done)
	}
	return permits
}
