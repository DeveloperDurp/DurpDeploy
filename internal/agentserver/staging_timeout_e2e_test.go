//go:build e2e && agentstaging

package agentserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDeploymentStagingTransferPausesStepTimeoutAPIE2E(t *testing.T) {
	for _, scenario := range []string{"download", "upload", "execution"} {
		t.Run(scenario, func(t *testing.T) {
			s := newStagingE2EWithTransfers(
				t,
				"",
				func(next http.Handler) http.Handler {
					return http.HandlerFunc(
						func(w http.ResponseWriter, r *http.Request) {
							if strings.HasSuffix(
								r.URL.Path,
								"/staging/"+scenario,
							) {
								pause := &stagingTransferPause{ctx: r.Context()}
								if scenario == "download" {
									w = &pausedStagingWriter{
										ResponseWriter: w,
										pause:          pause,
									}
								} else {
									r.Body = &pausedStagingReader{
										ReadCloser: r.Body,
										pause:      pause,
									}
								}
							}
							next.ServeHTTP(w, r)
						},
					)
				},
			)
			var project, environment, release, deployment struct{ ID int64 }
			decode := func(data []byte, target any) {
				t.Helper()
				if err := json.Unmarshal(data, target); err != nil {
					t.Fatal(err)
				}
			}
			decode(
				s.api(
					t,
					"POST",
					"/projects",
					map[string]string{"name": scenario},
					201,
				),
				&project,
			)
			decode(
				s.api(
					t,
					"POST",
					"/environments",
					map[string]string{"name": scenario},
					201,
				),
				&environment,
			)
			s.api(t, "POST", "/admin/agents/test-agent/environments",
				map[string]int64{"environment_id": environment.ID}, 204)
			base := fmt.Sprintf("/projects/%d", project.ID)
			script, want := "echo transfer-ok", "succeeded"
			if scenario == "execution" {
				script, want = "sleep 10; echo timeout-missed", "failed"
			}
			s.api(t, "POST", base+"/steps", map[string]any{
				"name": "remote", "script_body": script, "interpreter": "bash",
				"execution_target": "agent", "agent_execution_mode": "host",
				"agent_selectors": []string{"test-agent"}, "timeout_seconds": 3,
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
			decode(s.api(t, "POST", base+"/deployments", map[string]int64{
				"release_id": release.ID, "environment_id": environment.ID,
			}, 201), &deployment)
			s.wait(t, deployment.ID, want)
			s.assertRuntimeStagingPurged(t)
			s.assertTransferPurged(t, scenario, deployment.ID)
		})
	}
}

type stagingTransferPause struct {
	ctx  context.Context
	done bool
}

func (p *stagingTransferPause) wait() error {
	if p.done {
		return nil
	}
	p.done = true
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-p.ctx.Done():
		return p.ctx.Err()
	}
}

type pausedStagingWriter struct {
	http.ResponseWriter
	pause *stagingTransferPause
}

func (w *pausedStagingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *pausedStagingWriter) Write(data []byte) (int, error) {
	if err := w.pause.wait(); err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(data)
}

type pausedStagingReader struct {
	io.ReadCloser
	pause *stagingTransferPause
}

func (r *pausedStagingReader) Read(data []byte) (int, error) {
	if err := r.pause.wait(); err != nil {
		return 0, err
	}
	return r.ReadCloser.Read(data)
}
