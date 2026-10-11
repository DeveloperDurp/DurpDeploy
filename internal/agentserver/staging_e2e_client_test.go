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
	deadline := time.Now().Add(3 * time.Minute)
	for {
		var deployment struct{ Status string }
		if err := json.Unmarshal(s.api(t, "GET", fmt.Sprintf("/deployments/%d", id), nil, 200), &deployment); err != nil {
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
		time.Sleep(100 * time.Millisecond)
	}
}
