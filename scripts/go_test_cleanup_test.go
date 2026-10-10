//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

func TestCleanupConcurrentRemoval(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		status     int
		disappears bool
		wantError  bool
	}{
		{"reaper finishes", http.StatusConflict, true, false},
		{"removal stalls", http.StatusConflict, false, true},
		{"removal denied", http.StatusForbidden, false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given: both owned and unrelated containers in the API response.
			session := "durpdeploy-go-test.fixture"
			var inspections, deletions atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case strings.HasSuffix(r.URL.Path, "/containers/json"):
						json.NewEncoder(w).Encode([]map[string]any{
							{"Id": "owned", "Labels": map[string]string{
								"org.testcontainers":           "true",
								"org.testcontainers.sessionId": session,
							}},
							{"Id": "unrelated", "Labels": map[string]string{
								"org.testcontainers":           "true",
								"org.testcontainers.sessionId": "another-run",
							}},
						})
					case r.Method == http.MethodDelete:
						deletions.Add(1)
						if !strings.HasSuffix(r.URL.Path, "/containers/owned") {
							t.Errorf(
								"removed unrelated container: %s",
								r.URL.Path,
							)
						}
						w.WriteHeader(scenario.status)
						json.NewEncoder(w).Encode(map[string]string{
							"message": "removal is already in progress",
						})
					case strings.HasSuffix(r.URL.Path, "/containers/owned/json"):
						count := inspections.Add(1)
						if scenario.disappears && count > 1 {
							w.WriteHeader(http.StatusNotFound)
						}
						json.NewEncoder(w).Encode(map[string]string{})
					default:
						t.Errorf("unexpected API request: %s", r.URL.Path)
						w.WriteHeader(http.StatusBadRequest)
					}
				}))
			defer server.Close()
			api, err := client.New(client.WithHost(server.URL),
				client.WithAPIVersion("1.44"))
			require.NoError(t, err)
			defer func() { require.NoError(t, api.Close()) }()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()

			// When: the SDK reports a concurrent deletion or a real failure.
			err = cleanup(ctx, &testcontainers.DockerClient{Client: api},
				[]string{session})

			// Then: wait for confirmed removal, preserve errors and ownership.
			if scenario.wantError {
				require.Error(t, err)
				if scenario.status == http.StatusConflict {
					require.ErrorIs(t, err, context.DeadlineExceeded)
				}
			} else {
				require.NoError(t, err)
				require.Greater(t, inspections.Load(), int32(1))
			}
			require.Equal(t, int32(1), deletions.Load())
		})
	}
}
