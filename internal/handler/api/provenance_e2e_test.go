//go:build e2e

package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/notify"
	"durpdeploy/internal/provenance"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
	"durpdeploy/internal/testutil"
	"github.com/robfig/cron/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func provenanceDSN(t *testing.T, backend string) string {
	t.Helper()
	switch backend {
	case "SQLite":
		return "file:" + filepath.Join(t.TempDir(), "provenance.db") +
			"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	case "SQLServer":
		return testutil.SQLServerDSN(t)
	default:
		container, err := postgres.Run(
			t.Context(),
			"harbor.durp.info/dockerhub/library/postgres:16-alpine",
			postgres.WithDatabase(
				"provenance",
			),
			postgres.WithUsername("provenance"),
			postgres.WithPassword(
				"fixture-only",
			),
			postgres.BasicWaitStrategies(),
		)
		testutil.CleanupContainer(t, container)
		if err != nil {
			t.Fatal(err)
		}
		dsn, err := container.ConnectionString(t.Context(), "sslmode=disable")
		if err != nil {
			t.Fatal(err)
		}
		return dsn
	}
}

type provenanceDiscordTransport struct{ messages chan string }

func (transport provenanceDiscordTransport) RoundTrip(
	r *http.Request,
) (*http.Response, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, err
	}
	var full string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, err
		}
		switch part.FormName() {
		case "payload_json":
			var payload struct{ Content string }
			if err := json.Unmarshal(data, &payload); err != nil {
				return nil, err
			}
			if len(payload.Content) > 2000 {
				return nil, fmt.Errorf("oversized Discord content")
			}
		case "files[0]":
			full = string(data)
		}
	}
	transport.messages <- full
	return &http.Response{
		StatusCode: 204,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}, nil
}

func TestReleaseProvenanceMaximumNotificationE2E(t *testing.T) {
	f := newArtifactE2E(t)
	messages := make(chan string, 8)
	bus := events.NewBus(f.h.repo)
	bus.Register(notify.NewDiscordNotifierWithClient(&http.Client{
		Transport: provenanceDiscordTransport{messages},
	}))
	bus.Register(artifactNotifier{f.done})
	f.h.runner.SetEventBus(bus)
	// The test transport handles delivery. No external webhook receives a message.
	f.api(t, "PUT", f.base()+"/notifications", map[string]string{
		"discord_webhook_url": "https://discord.com/api/webhooks/fixture-18",
	}, 200)
	source := publicSource()
	source.RepositoryURL = "https://example.test/" + strings.Repeat("r", 2027)
	source.CIRunURL = "https://ci.example.test/" + strings.Repeat("c", 2024)
	source.ArtifactRef = strings.Repeat("a", 2048)
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases", struct {
		Version string `json:"version"`
		provenance.Source
	}{"maximum-source", source}, 201), &release)
	var deployment db.Deployment
	decodeStepLogTest(
		t,
		f.api(t, "POST", f.base()+"/deployments", map[string]int64{
			"release_id": release.ID, "environment_id": f.environment.ID,
		}, 201),
		&deployment,
	)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	for range 2 {
		select {
		case message := <-messages:
			for _, value := range []string{source.RepositoryURL, source.CommitSHA, source.ArtifactRef, source.CIRunURL} {
				if !strings.Contains(message, value) {
					t.Fatal("long Discord notification lost provenance")
				}
			}
		case <-time.After(time.Second):
			t.Fatal("Discord notification was not delivered")
		}
	}
}

func newProvenanceE2E(t *testing.T, dsn string) (*artifactE2E, func()) {
	t.Helper()
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.New(conn)
	user := seedAPIUser(t, repo, "provenance@example.test", "admin")
	_, token := seedAPIToken(t, repo, user.ID)
	f := &artifactE2E{h: &harness{repo: repo}, token: token,
		csrf: "member-role-csrf", client: &http.Client{
			Timeout:       time.Minute,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}}
	f.session = memberRoleSession(t, f.h, user.ID)
	f.project, f.environment = seedProject(t, repo), seedEnv(t, repo)
	var srv *httptest.Server
	start := func() {
		repo = repository.New(conn)
		rnr := runner.New(repo, runner.NewLogBroker())
		f.h = &harness{repo: repo, runner: rnr}
		srv = httptest.NewServer(server.NewRouter(repo, rnr,
			cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
			handler.NewAuthHandler(repo)))
		f.server, f.baseURL = srv, srv.URL
	}
	start()
	t.Cleanup(func() { srv.Close(); testutil.Close(t, conn) })
	return f, func() {
		srv.Close()
		testutil.Close(t, conn)
		// Open through migration dispatch, as an actual server restart does.
		conn, err = migrate.Run(dsn)
		if err != nil {
			t.Fatal(err)
		}
		start()
	}
}

func publicSource() provenance.Source {
	return provenance.Source{
		RepositoryURL: "https://git.example.test/team/app.git",
		CommitSHA:     strings.Repeat("a", 40),
		ArtifactRef: "registry.example.test/app@sha256:" + strings.Repeat(
			"b",
			64,
		),
		CIRunURL: "https://ci.example.test/runs/42",
	}
}

type idempotentResponse struct {
	body     []byte
	status   int
	replayed string
	err      error
}

func sendIdempotent(t *testing.T, f *artifactE2E,
	path, key string, body any,
) idempotentResponse {
	data, err := json.Marshal(body)
	if err != nil {
		return idempotentResponse{err: err}
	}
	req, err := http.NewRequestWithContext(t.Context(), "POST",
		f.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return idempotentResponse{err: err}
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, err := f.client.Do(req)
	if err != nil {
		return idempotentResponse{err: err}
	}
	defer resp.Body.Close()
	result, err := io.ReadAll(resp.Body)
	t.Logf("POST %s request=%s status=%d headers=%v body=%s",
		path, data, resp.StatusCode, resp.Header, result)
	return idempotentResponse{result, resp.StatusCode,
		resp.Header.Get("Idempotency-Replayed"), err}
}

func requireIdempotent(
	t *testing.T,
	response idempotentResponse,
	want int,
) []byte {
	t.Helper()
	if response.err != nil || response.status != want {
		t.Fatalf("idempotent response: status=%d want=%d err=%v body=%s",
			response.status, want, response.err, response.body)
	}
	return response.body
}

func TestReleaseProvenanceIdempotencyAPIWebE2E(t *testing.T) {
	for _, backend := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(backend, func(t *testing.T) {
			f, restart := newProvenanceE2E(t, provenanceDSN(t, backend))
			source := publicSource()
			body := struct {
				Version string `json:"version"`
				provenance.Source
			}{"ci-v1", source}
			path := f.base() + "/releases"
			original := concurrentCreation(t, f, path, "release-key", body)
			var release db.Release
			decodeStepLogTest(t, original, &release)
			assertProvenance(t, f.api(t, "GET",
				fmt.Sprintf("%s/%d", path, release.ID), nil, 200), source)
			// Refresh can replace steps and variables, but never source provenance.
			assertProvenance(t, f.api(
				t,
				"POST",
				fmt.Sprintf(
					"%s/%d/refresh",
					path,
					release.ID,
				),
				nil,
				200,
			), source)
			body.Version = "ci-v2"
			requireIdempotent(
				t,
				sendIdempotent(t, f, path, "release-key", body),
				409,
			)
			body.Version = "ci-v1"
			restart()
			replayed := sendIdempotent(t, f, path, "release-key", body)
			if !bytes.Equal(original, requireIdempotent(t, replayed, 201)) ||
				replayed.replayed != "true" {
				t.Fatal("restart did not return the original release response")
			}
			checkDuplicateReleaseCount(t, f, 1)
			checkProvenanceWeb(t, f, release, source)
			checkProvenanceRejections(t, f)
			checkDeploymentIdempotency(t, f, restart, release)
			checkIdempotencyRollback(t, f)
			checkIdempotencyPermissions(t, f, body)
		})
	}
}

func checkIdempotencyRollback(t *testing.T, f *artifactE2E) {
	t.Helper()
	path := f.base() + "/releases"
	body := map[string]string{"version": "ci-v1"}
	requireIdempotent(t, sendIdempotent(t, f, path, "rollback-key", body), 409)
	body["version"] = "rollback-v1"
	var release db.Release
	decodeStepLogTest(t, requireIdempotent(t,
		sendIdempotent(t, f, path, "rollback-key", body), 201), &release)
	// Force failure after claiming a deployment key and before committing.
	_, err := f.h.repo.Queries.UpdateRelease(
		t.Context(),
		db.UpdateReleaseParams{
			ID: release.ID, ProjectID: f.project.ID, Version: release.Version,
			StepsJson: "invalid-json",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deploy := map[string]int64{
		"release_id":     release.ID,
		"environment_id": f.environment.ID,
	}
	path = f.base() + "/deployments"
	requireIdempotent(
		t,
		sendIdempotent(t, f, path, "rollback-key", deploy),
		500,
	)
	_, err = f.h.repo.Queries.UpdateRelease(t.Context(), db.UpdateReleaseParams{
		ID: release.ID, ProjectID: f.project.ID, Version: release.Version,
		StepsJson: release.StepsJson,
	})
	if err != nil {
		t.Fatal(err)
	}
	requireIdempotent(
		t,
		sendIdempotent(t, f, path, "rollback-key", deploy),
		201,
	)
}

func checkIdempotencyPermissions(t *testing.T, f *artifactE2E, body any) {
	t.Helper()
	path := f.base() + "/releases"
	other := *f
	admin := seedAPIUser(t, f.h.repo, "other-provenance@example.test", "admin")
	_, other.token = seedAPIToken(t, f.h.repo, admin.ID)
	// A different principal cannot replay another principal's creation.
	requireIdempotent(
		t,
		sendIdempotent(t, &other, path, "release-key", body),
		409,
	)
	viewer := seedAPIUser(
		t,
		f.h.repo,
		"provenance-viewer@example.test",
		"viewer",
	)
	if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{
		ProjectID: f.project.ID, UserID: viewer.ID, Role: "viewer",
	}); err != nil {
		t.Fatal(err)
	}
	_, other.token = seedAPIToken(t, f.h.repo, viewer.ID)
	requireIdempotent(
		t,
		sendIdempotent(t, &other, path, "release-key", body),
		403,
	)
	var release db.Release
	decodeStepLogTest(t, f.api(t, "GET", path+"/1", nil, 200), &release)
	assertProvenance(
		t,
		other.api(t, "GET", fmt.Sprintf("%s/%d", path, release.ID), nil, 200),
		publicSource(),
	)
}

func TestReleaseProvenanceExecutionE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "CI provenance", "script_body": "printf 'provenance-once\\n'",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}, 201)
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases", struct {
		Version string `json:"version"`
		provenance.Source
	}{"execute-v1", publicSource()}, 201), &release)
	body := map[string]int64{
		"release_id":     release.ID,
		"environment_id": f.environment.ID,
	}
	original := concurrentCreation(
		t,
		f,
		f.base()+"/deployments",
		"execute-key",
		body,
	)
	var deployment db.Deployment
	decodeStepLogTest(t, original, &deployment)
	select {
	case event := <-f.done:
		if event.Type != events.DeploymentSucceeded ||
			!strings.Contains(event.Message, publicSource().CommitSHA) ||
			!strings.Contains(event.Message, publicSource().CIRunURL) {
			t.Fatalf(
				"notification omitted source or execution failed: %+v",
				event,
			)
		}
	case <-time.After(time.Minute):
		t.Fatal("execution did not finish")
	}
	if !bytes.Equal(original, requireIdempotent(t, sendIdempotent(t, f,
		f.base()+"/deployments", "execute-key", body), 201)) {
		t.Fatal("terminal deployment replay changed original response")
	}
	var logs []struct{ Line string }
	decodeStepLogTest(t, f.api(
		t,
		"GET",
		fmt.Sprintf(
			"/api/v1/deployments/%d/logs",
			deployment.ID,
		),
		nil,
		200,
	), &logs)
	count := 0
	for _, log := range logs {
		if log.Line == "provenance-once" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("execution output count=%d want=1", count)
	}
	var details string
	if err := f.h.repo.DB.QueryRowContext(t.Context(),
		"SELECT details FROM audit_log WHERE action = 'create_deployment'").Scan(&details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, publicSource().CommitSHA) {
		t.Fatal("audit omitted provenance")
	}
}

func concurrentCreation(t *testing.T, f *artifactE2E,
	path, key string, body any,
) []byte {
	t.Helper()
	start := make(chan struct{})
	responses := make(chan idempotentResponse, 6)
	var workers sync.WaitGroup
	for range cap(responses) {
		workers.Go(
			func() { <-start; responses <- sendIdempotent(t, f, path, key, body) },
		)
	}
	close(start)
	workers.Wait()
	close(responses)
	var original []byte
	replayed := 0
	for response := range responses {
		body := requireIdempotent(t, response, 201)
		if original == nil {
			original = body
		} else if !bytes.Equal(original, body) {
			t.Fatal("concurrent retries returned different responses")
		}
		if response.replayed == "true" {
			replayed++
		}
	}
	if replayed != 5 {
		t.Fatalf("replayed=%d want=5", replayed)
	}
	return original
}

func assertProvenance(t *testing.T, body []byte, want provenance.Source) {
	t.Helper()
	var got provenance.Source
	decodeStepLogTest(t, body, &got)
	if got != want {
		t.Fatalf("source=%+v want=%+v", got, want)
	}
}

func checkProvenanceWeb(t *testing.T, f *artifactE2E,
	release db.Release, source provenance.Source,
) {
	t.Helper()
	webPath := strings.TrimPrefix(f.base(), "/api/v1") + "/releases"
	page := f.web(t, "GET", fmt.Sprintf("%s/%d", webPath, release.ID), nil, 200)
	for _, value := range []string{source.RepositoryURL, source.CommitSHA,
		source.ArtifactRef, source.CIRunURL} {
		if !strings.Contains(page, value) {
			t.Fatal("release page omitted provenance")
		}
	}
	form := url.Values{"version": {"web-v1"},
		"repository_url": {
			source.RepositoryURL,
		}, "commit_sha": {source.CommitSHA},
		"artifact_ref": {source.ArtifactRef}, "ci_run_url": {source.CIRunURL}}
	f.web(t, "POST", webPath, form, 303)
	duplicate := f.web(t, "POST", webPath, form, 422)
	if !strings.Contains(duplicate, source.RepositoryURL) {
		t.Fatal("duplicate version discarded safe provenance form fields")
	}
}

func checkProvenanceRejections(t *testing.T, f *artifactE2E) {
	t.Helper()
	path := f.base() + "/releases"
	for _, field := range []string{"repository_url", "artifact_ref", "ci_run_url"} {
		body := map[string]string{
			"version": "unsafe",
			field:     "https://user:provenance-credential@example.test/file",
		}
		response := f.api(t, "POST", path, body, 422)
		page := f.web(t, "POST", strings.TrimPrefix(path, "/api/v1"),
			url.Values{"version": {"unsafe"}, field: {body[field]}}, 422)
		if strings.Contains(string(response)+page, "provenance-credential") {
			t.Fatal("credential echoed in rejected API/web input")
		}
	}
	for _, key := range []string{"", strings.Repeat("a", 256), "bad key"} {
		requireIdempotent(t, sendIdempotent(t, f, path, key,
			map[string]string{"version": "bad-key"}), 400)
	}
}

func checkDeploymentIdempotency(t *testing.T, f *artifactE2E,
	restart func(), release db.Release,
) {
	t.Helper()
	// An approval-gated deployment stays pending across a real database reopen.
	var lifecycle struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "CI approval"}, 201), &lifecycle)
	f.api(t, "POST", fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID),
		map[string]any{"environment_id": f.environment.ID, "sort_order": 1,
			"requires_approval": true}, 201)
	f.api(t, "PUT", f.base(), map[string]any{
		"name": f.project.Name, "lifecycle_id": lifecycle.ID}, 200)
	body := map[string]int64{
		"release_id":     release.ID,
		"environment_id": f.environment.ID,
	}
	path := f.base() + "/deployments"
	original := concurrentCreation(t, f, path, "deployment-key", body)
	var deployment db.Deployment
	decodeStepLogTest(t, original, &deployment)
	if deployment.Status != "pending_approval" {
		t.Fatal("approval gate bypassed")
	}
	var count int
	if err := f.h.repo.DB.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM deployments").Scan(&count); err != nil || count != 1 {
		t.Fatalf("deployment count=%d err=%v", count, err)
	}
	restart()
	if !bytes.Equal(original, requireIdempotent(t,
		sendIdempotent(t, f, path, "deployment-key", body), 201)) {
		t.Fatal("restart did not return original deployment")
	}
	read := f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d", deployment.ID),
		nil,
		200,
	)
	var detail struct {
		Provenance provenance.Source `json:"provenance"`
	}
	decodeStepLogTest(t, read, &detail)
	if detail.Provenance != publicSource() {
		t.Fatal("deployment API omitted provenance")
	}
	page := f.web(
		t,
		"GET",
		fmt.Sprintf("/deployments/%d", deployment.ID),
		nil,
		200,
	)
	if !strings.Contains(page, publicSource().CommitSHA) {
		t.Fatal("deployment page omitted provenance")
	}
	body["environment_id"]++
	requireIdempotent(
		t,
		sendIdempotent(t, f, path, "deployment-key", body),
		409,
	)
	var audits int
	if err := f.h.repo.DB.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM audit_log WHERE action = 'create_deployment'").Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("creation audit count=%d error=%v", audits, err)
	}
}
