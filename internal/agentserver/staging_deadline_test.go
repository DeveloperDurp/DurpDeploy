package agentserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAgentStagingSurvivesListenerDeadlines(t *testing.T) {
	for _, scenario := range []string{
		"upload-read", "upload-response", "download-write",
	} {
		t.Run(scenario, func(t *testing.T) {
			// Given a live staging claim behind the real mTLS router.
			f := newAgentFixture(t)
			id, claim := claimedRemoteStep(t, f)
			seedStageInput(t, f, id)
			var identity struct {
				Token string `json:"claim_token"`
			}
			if err := json.Unmarshal([]byte(claim), &identity); err != nil {
				t.Fatal(err)
			}
			response := postAgent(t, f,
				fmt.Sprintf("/agent/v1/deployments/%d/start", id), claim)
			response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				t.Fatal(response.StatusCode)
			}
			started := make(chan struct{})
			router := f.server.Config.Handler
			listener := httptest.NewUnstartedServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					close(started)
					router.ServeHTTP(&delayedStagingResponse{
						ResponseWriter: w, ctx: r.Context(), scenario: scenario,
					}, r)
				},
			))
			listener.TLS = f.server.TLS.Clone()
			listener.Config.ReadTimeout = 100 * time.Millisecond
			listener.Config.WriteTimeout = 100 * time.Millisecond
			listener.StartTLS()
			defer listener.Close()
			action := "upload"
			want := http.StatusNoContent
			data := stageArchive(t, map[string]string{"output": "shared"})
			var body io.Reader = bytes.NewReader(data)
			if scenario == "upload-read" {
				body = &delayedStagingBody{
					Reader: bytes.NewReader(
						data,
					),
					ctx:     t.Context(),
					started: started,
				}
			}
			if scenario == "download-write" {
				action, want, body = "download", http.StatusOK, nil
			}
			request, err := http.NewRequestWithContext(t.Context(), "POST",
				fmt.Sprintf("%s/agent/v1/deployments/%d/staging/%s",
					listener.URL, id, action), body)
			if err != nil {
				t.Fatal(err)
			}
			if body != nil {
				request.ContentLength = int64(len(data))
			}
			request.Header.Set("X-DurpDeploy-Claim", identity.Token)

			// When valid I/O takes longer than the listener's absolute deadline.
			response, err = f.client.Do(request)

			// Then the transfer and its final response remain valid.
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			contents, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != want {
				t.Fatalf("status=%d want=%d body=%q error=%v",
					response.StatusCode, want, contents, err)
			}
			if action == "download" &&
				!bytes.Contains(contents, []byte("staging-secret")) {
				t.Fatal("download lost staging contents")
			}
		})
	}
}

type delayedStagingBody struct {
	*bytes.Reader
	ctx     context.Context
	started <-chan struct{}
	delayed bool
}

func (r *delayedStagingBody) Read(data []byte) (int, error) {
	if !r.delayed {
		select {
		case <-r.started:
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		}
		if err := stagingDelay(r.ctx); err != nil {
			return 0, err
		}
		r.delayed = true
	}
	return r.Reader.Read(data)
}

type delayedStagingResponse struct {
	http.ResponseWriter
	ctx      context.Context
	scenario string
}

func (w *delayedStagingResponse) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *delayedStagingResponse) Write(data []byte) (int, error) {
	if w.scenario == "download-write" {
		if err := stagingDelay(w.ctx); err != nil {
			return 0, err
		}
	}
	return w.ResponseWriter.Write(data)
}

func (w *delayedStagingResponse) WriteHeader(status int) {
	if w.scenario == "upload-response" {
		if err := stagingDelay(w.ctx); err != nil {
			return
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func stagingDelay(ctx context.Context) error {
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
