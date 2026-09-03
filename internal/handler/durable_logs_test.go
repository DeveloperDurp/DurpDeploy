package handler

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"github.com/go-chi/chi/v5"
)

func TestStreamLogs_ReplaysOnlyLogsAfterLastEventID(t *testing.T) {
	// Given
	broker := runner.NewLogBroker()
	repo := setupTestRepo(t)
	deployment := durableLogDeployment(t, repo)
	first := durableLog(
		t,
		repo,
		durableLogInput{deploymentID: deployment.ID, line: "first"},
	)
	second := durableLog(
		t,
		repo,
		durableLogInput{deploymentID: deployment.ID, line: "second"},
	)
	third := durableLog(
		t,
		repo,
		durableLogInput{deploymentID: deployment.ID, line: "third"},
	)

	router := chi.NewRouter()
	router.Get(
		"/deployments/{id}/logs/stream",
		NewLogHandler(broker, repo).StreamLogs,
	)
	server := httptest.NewServer(router)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/deployments/%d/logs/stream", server.URL, deployment.ID),
		nil,
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Last-Event-ID", fmt.Sprint(first.ID))

	// When
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream logs: %v", err)
	}
	defer response.Body.Close()
	got := readSSEEvents(t, response.Body, 2)

	// Then
	want := []string{
		fmt.Sprintf("id: %d\ndata: second", second.ID),
		fmt.Sprintf("id: %d\ndata: third", third.ID),
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

func TestStreamLogs_ordersReversedRemoteSequences(t *testing.T) {
	// Given: remote logs persisted in arrival order 2 then 1.
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

	// When: the durable stream replays its history.
	response, err := http.Get(
		fmt.Sprintf("%s/deployments/%d/logs/stream", server.URL, deployment.ID),
	)
	if err != nil {
		t.Fatalf("stream logs: %v", err)
	}
	defer response.Body.Close()
	got := readSSEEvents(t, response.Body, 2)

	// Then: sequence order wins over insertion order.
	if !strings.Contains(got[0], "data: one") ||
		!strings.Contains(got[1], "data: two") {
		t.Fatalf("events = %q, want one then two", got)
	}
}

func TestStreamLogs_ReadsPersistedRowsOnce_whenWakeupsDropOrDuplicate(
	t *testing.T,
) {
	// Given
	broker := runner.NewLogBroker()
	repo := setupTestRepo(t)
	deployment := durableLogDeployment(t, repo)
	first := durableLog(
		t,
		repo,
		durableLogInput{deploymentID: deployment.ID, line: "history"},
	)
	if first.Sequence != 1 {
		t.Fatalf("first sequence = %d, want 1", first.Sequence)
	}
	router := chi.NewRouter()
	router.Get(
		"/deployments/{id}/logs/stream",
		NewLogHandler(broker, repo).StreamLogs,
	)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/deployments/%d/logs/stream", server.URL, deployment.ID),
		nil,
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream logs: %v", err)
	}
	defer response.Body.Close()
	reader := newSSEEventReader(response.Body)
	if event := reader.next(
		t,
	); event != fmt.Sprintf(
		"id: %d\ndata: history",
		first.ID,
	) {
		t.Fatalf("history event = %q", event)
	}
	second := durableLog(
		t,
		repo,
		durableLogInput{deploymentID: deployment.ID, line: "dropped wakeup"},
	)
	third := durableLog(
		t,
		repo,
		durableLogInput{deploymentID: deployment.ID, line: "duplicate wakeup"},
	)
	if second.Sequence != 2 || third.Sequence != 3 {
		t.Fatalf(
			"live sequences = %d, %d; want 2, 3",
			second.Sequence,
			third.Sequence,
		)
	}

	// When
	broker.Broadcast(deployment.ID, third.ID)
	broker.Broadcast(deployment.ID, third.ID)

	// Then
	if event := reader.next(
		t,
	); event != fmt.Sprintf(
		"id: %d\ndata: dropped wakeup",
		second.ID,
	) {
		t.Fatalf("first live event = %q", event)
	}
	if event := reader.next(
		t,
	); event != fmt.Sprintf(
		"id: %d\ndata: duplicate wakeup",
		third.ID,
	) {
		t.Fatalf("second live event = %q", event)
	}
}

func durableLogDeployment(
	t *testing.T,
	repo *repository.Repository,
) db.Deployment {
	t.Helper()
	ctx := context.Background()
	project, err := repo.Queries.CreateProject(ctx, db.CreateProjectParams{
		Name: "durable-log-project", Description: sql.NullString{},
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{
			Name: "durable-log-environment", Description: sql.NullString{}, Tags: sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "durable-log-release", StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "running",
			StartedAt: sql.NullInt64{
				Int64: time.Now().Unix(),
				Valid: true,
			}, Forced: 0,
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	return deployment
}

type durableLogInput struct {
	deploymentID int64
	line         string
}

func durableLog(
	t *testing.T,
	repo *repository.Repository,
	input durableLogInput,
) db.DeploymentLog {
	t.Helper()
	log, err := repo.Queries.CreateDeploymentLog(
		context.Background(),
		db.CreateDeploymentLogParams{
			DeploymentID: input.deploymentID,
			Line:         input.line,
		},
	)
	if err != nil {
		t.Fatalf("create deployment log: %v", err)
	}
	return log
}

func readSSEEvents(t *testing.T, body io.Reader, count int) []string {
	t.Helper()
	scanner := bufio.NewScanner(body)
	events := make([]string, 0, count)
	var event []string
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			event = append(event, line)
			continue
		}
		events = append(events, strings.Join(event, "\n"))
		event = nil
		if len(events) == count {
			return events
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read SSE events: %v", err)
	}
	t.Fatalf("received %d SSE events, want %d", len(events), count)
	return nil
}

type sseEventReader struct {
	scanner *bufio.Scanner
}

func newSSEEventReader(body io.Reader) sseEventReader {
	return sseEventReader{scanner: bufio.NewScanner(body)}
}

func (r sseEventReader) next(t *testing.T) string {
	t.Helper()
	var event []string
	for r.scanner.Scan() {
		line := r.scanner.Text()
		if line == "" {
			return strings.Join(event, "\n")
		}
		event = append(event, line)
	}
	if err := r.scanner.Err(); err != nil {
		t.Fatalf("read SSE event: %v", err)
	}
	t.Fatal("SSE stream ended before event")
	return ""
}
