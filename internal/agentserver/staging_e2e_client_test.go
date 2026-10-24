//go:build e2e && agentstaging

package agentserver_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

func (s *stagingE2E) api(
	t *testing.T,
	method, path string,
	body any,
	want int,
) []byte {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequestWithContext(
		t.Context(),
		method,
		s.app.URL+"/api/v1"+path,
		bytes.NewReader(data),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+s.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err = io.ReadAll(response.Body)
	if err != nil || response.StatusCode != want {
		t.Fatalf(
			"%s %s status=%d want=%d err=%v body=%s",
			method,
			path,
			response.StatusCode,
			want,
			err,
			data,
		)
	}
	return data
}

func (s *stagingE2E) wait(t *testing.T, id int64, want string) {
	t.Helper()
	s.waitFor(t, id, want, 3*time.Minute, 100*time.Millisecond)
}

func (s *stagingE2E) waitFor(
	t *testing.T, id int64, want string, timeout, interval time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var deployment struct{ Status string }
		if err := json.Unmarshal(
			s.api(t, "GET", fmt.Sprintf("/deployments/%d", id), nil, 200),
			&deployment,
		); err != nil {
			t.Fatal(err)
		}
		if deployment.Status == want {
			return
		}
		if deployment.Status == "failed" ||
			deployment.Status == "cleanup_unconfirmed" ||
			time.Now().After(deadline) {
			t.Fatalf(
				"deployment %d status=%s want=%s logs=%s",
				id,
				deployment.Status,
				want,
				s.api(
					t,
					"GET",
					fmt.Sprintf("/deployments/%d/logs.txt", id),
					nil,
					200,
				),
			)
		}
		time.Sleep(interval)
	}
}

func (s *stagingE2E) cancelRunningStaging(t *testing.T, deploymentID int64) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		var started int
		if err := s.f.repo.DB.QueryRow("SELECT COUNT(*) FROM remote_step_runs WHERE deployment_id=? AND state='started'", deploymentID).
			Scan(&started); err != nil {
			t.Fatal(err)
		}
		if started == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agents did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.api(
		t,
		"POST",
		fmt.Sprintf("/deployments/%d/cancel", deploymentID),
		nil,
		200,
	)
}
