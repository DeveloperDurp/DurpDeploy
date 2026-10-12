//go:build e2e

package api_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/httpstream"
)

func TestDeploymentStepLogOutcomesAPIWebE2E(t *testing.T) {
	for _, test := range []struct {
		name, script, states string
		retries              int
		cancel               bool
	}{
		{"failure", "exit 4", "running,failed", 0, false},
		{"retry", `if [ ! -e "$DURPDEPLOY_STAGE_DIR/once" ]; then touch "$DURPDEPLOY_STAGE_DIR/once"; exit 1; fi`, "running,failed,running,succeeded", 1, false},
		{"cancellation", "sleep 60", "running,cancelled", 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newArtifactE2E(t)
			f.api(t, "POST", f.base()+"/steps", map[string]any{
				"name": test.name, "script_body": test.script,
				"container_image": "docker.io/library/bash:5.2", "max_retries": test.retries,
			}, 201)
			var release db.Release
			decodeStepLogTest(
				t,
				f.api(
					t,
					"POST",
					f.base()+"/releases",
					map[string]string{"version": test.name},
					201,
				),
				&release,
			)
			var deployment db.Deployment
			decodeStepLogTest(
				t,
				f.api(
					t,
					"POST",
					f.base()+"/deployments",
					map[string]int64{
						"release_id":     release.ID,
						"environment_id": f.environment.ID,
					},
					201,
				),
				&deployment,
			)
			if test.cancel {
				deadline := time.Now().Add(15 * time.Second)
				for {
					logs, err := f.h.repo.Queries.ListDeploymentLogsByDeployment(
						t.Context(),
						deployment.ID,
					)
					if err != nil {
						t.Fatal(err)
					}
					started := false
					for _, log := range logs {
						if log.StepState.String == "running" {
							started = true
						}
					}
					if started {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("step never started")
					}
					time.Sleep(20 * time.Millisecond)
				}
				f.api(
					t,
					"POST",
					fmt.Sprintf("/api/v1/deployments/%d/cancel", deployment.ID),
					nil,
					200,
				)
			}
			logs := f.readStepLogs(
				t,
				fmt.Sprintf(
					"/api/v1/deployments/%d/logs/stream?format=structured",
					deployment.ID,
				),
				"",
			)
			var states []string
			for _, log := range logs {
				if log.State != "" {
					states = append(states, log.State)
				}
			}
			if strings.Join(states, ",") != test.states {
				t.Fatalf("states=%v, expected %s", states, test.states)
			}
			page := f.web(
				t,
				"GET",
				fmt.Sprintf("/deployments/%d", deployment.ID),
				nil,
				200,
			)
			if !strings.Contains(page, `data-step-index="0"`) {
				t.Fatal("terminal outcome lost its step panel")
			}
		})
	}
}

func decodeStepLogTest(t *testing.T, data []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func (f *artifactE2E) readStepLogs(
	t *testing.T,
	path, last string,
) []httpstream.StepLog {
	t.Helper()
	req, err := http.NewRequestWithContext(
		t.Context(),
		"GET",
		f.baseURL+path,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	if last != "" {
		req.Header.Set("Last-Event-ID", last)
	}
	response, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("stream status=%d", response.StatusCode)
	}
	var logs []httpstream.StepLog
	complete := false
	event := ""
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if value, ok := strings.CutPrefix(line, "event: "); ok {
			event = value
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			switch event {
			case "log":
				var log httpstream.StepLog
				decodeStepLogTest(t, []byte(data), &log)
				logs = append(logs, log)
			case "complete":
				complete = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Fatal("terminal stream did not drain and complete")
	}
	return logs
}
