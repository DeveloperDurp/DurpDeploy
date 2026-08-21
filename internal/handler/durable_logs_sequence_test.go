package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/runner"
	"github.com/go-chi/chi/v5"
)

func TestStreamLogs_usesSequenceForOrderedRemoteReplayCursor(t *testing.T) {
	// Given: remote logs arrive out of sequence and receive different row IDs.
	broker := runner.NewLogBroker()
	repo := setupTestRepo(t)
	deployment := durableLogDeployment(t, repo)
	for _, log := range []struct {
		sequence int64
		line     string
	}{{2, "two"}, {1, "one"}} {
		if _, err := repo.DB.ExecContext(
			context.Background(),
			"INSERT INTO deployment_logs (deployment_id, line, sequence) VALUES (?, ?, ?)",
			deployment.ID,
			log.line,
			log.sequence,
		); err != nil {
			t.Fatalf("insert sequence %d: %v", log.sequence, err)
		}
	}
	router := chi.NewRouter()
	router.Get(
		"/deployments/{id}/logs/stream",
		NewLogHandler(broker, repo).StreamLogs,
	)
	server := httptest.NewServer(router)
	defer server.Close()

	// When: a client starts a durable SSE replay.
	response, err := http.Get(
		fmt.Sprintf("%s/deployments/%d/logs/stream", server.URL, deployment.ID),
	)
	if err != nil {
		t.Fatalf("stream logs: %v", err)
	}
	defer response.Body.Close()
	got := readSSEEvents(t, response.Body, 2)

	// Then: SSE IDs match the ordered durable sequence, so reconnect cursors
	// cannot replay an already observed out-of-order row.
	want := []string{"id: 1\ndata: one", "id: 2\ndata: two"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
}
