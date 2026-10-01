package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/httpstream"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/require"
)

type streamFixture struct {
	router http.Handler
	broker *runner.LogBroker
	paths  []streamRoute
}

type streamRoute struct {
	path string
	id   int64
}

func newStreamFixture(t *testing.T) streamFixture {
	t.Helper()
	ctx := context.Background()
	conn, err := migrate.Run(filepath.Join(t.TempDir(), "streams.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	repo := repository.New(conn)
	user, err := repo.Queries.CreateUser(ctx, db.CreateUserParams{
		Email: "timeouts@example.com", Name: "Timeouts", Role: "admin",
		PasswordHash: "unused",
	})
	require.NoError(t, err)
	_, err = repo.Queries.CreateSession(ctx, db.CreateSessionParams{
		ID: "timeout-session", UserID: user.ID, CsrfToken: "csrf",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	_, err = repo.Queries.CreateApiToken(ctx, db.CreateApiTokenParams{
		ID: "timeout-token", UserID: user.ID, Name: "Timeouts",
		TokenPrefix: "ddp_pat_timeout", Scope: "global",
		TokenHash: auth.HashApiToken("ddp_pat_timeout"),
	})
	require.NoError(t, err)
	project, err := repo.Queries.CreateProject(ctx,
		db.CreateProjectParams{Name: "stream-timeouts"})
	require.NoError(t, err)
	env, err := repo.Queries.CreateEnvironment(ctx,
		db.CreateEnvironmentParams{Name: "stream-timeouts"})
	require.NoError(t, err)
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "1", StepsJson: "[]",
	})
	require.NoError(t, err)
	ordinary, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: env.ID, Status: "running",
		},
	)
	require.NoError(t, err)
	book, _, err := repo.SaveRunbook(ctx, repository.RunbookSave{
		ProjectID: project.ID,
		Name:      "stream-timeouts",
		StepsJSON: `[{"name":"check","script_body":"true","container_image":"alpine:3.20"}]`,
	})
	require.NoError(t, err)
	execution, deployment, err := repo.CreateRunbookExecution(ctx,
		repository.RunbookExecutionRequest{
			ProjectID: project.ID, RunbookID: book.ID, EnvironmentID: env.ID,
		})
	require.NoError(t, err)
	id := ordinary.ID
	runbookID := deployment.Deployment.ID
	for _, depID := range []int64{id, runbookID} {
		_, err = repo.Queries.CreateDeploymentLog(
			ctx,
			db.CreateDeploymentLogParams{
				DeploymentID: depID,
				Line:         "historical",
				StepName:     sql.NullString{},
			},
		)
		require.NoError(t, err)
	}
	broker := runner.NewLogBroker()
	rnr := runner.New(repo, broker)
	router := server.NewRouter(repo, rnr, cron.NewParser(cron.Minute),
		handler.NewAuthHandler(repo))
	return streamFixture{router: router, broker: broker, paths: []streamRoute{
		{fmt.Sprintf("/deployments/%d/logs/stream", id), id},
		{fmt.Sprintf("/api/v1/deployments/%d/logs/stream", id), id},
		{
			fmt.Sprintf("/api/v1/deployments/%d/logs/stream?format=ndjson", id),
			id,
		},
		{fmt.Sprintf("/api/v1/deployments/%d/events", id), id},
		{fmt.Sprintf("/projects/%d/runbooks/executions/%d/logs/stream",
			project.ID, execution.ID), runbookID},
		{fmt.Sprintf("/api/v1/projects/%d/runbook-executions/%d/logs/stream",
			project.ID, execution.ID), runbookID},
		{
			fmt.Sprintf(
				"/api/v1/projects/%d/runbook-executions/%d/logs/stream?format=ndjson",
				project.ID,
				execution.ID,
			),
			runbookID,
		},
	}}
}

func TestHTTPStreamsSurviveOrdinaryWriteTimeout(t *testing.T) {
	f := newStreamFixture(t)
	for _, route := range f.paths {
		t.Run(route.path, func(t *testing.T) {
			// Given: every supported streaming route behind the full
			// authentication/audit/logging/error middleware stack.
			done := make(chan struct{})
			srv := httptest.NewUnstartedServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					defer close(done)
					f.router.ServeHTTP(w, r)
				},
			))
			srv.Config = newHTTPServer("", srv.Config.Handler)
			srv.Config.WriteTimeout = 100 * time.Millisecond
			srv.Start()
			defer srv.Close()
			ctx, cancel := context.WithTimeout(
				context.Background(),
				3*time.Second,
			)
			defer cancel()
			req, err := http.NewRequestWithContext(
				ctx,
				"GET",
				srv.URL+route.path,
				nil,
			)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer ddp_pat_timeout")
			req.AddCookie(
				&http.Cookie{Name: "session", Value: "timeout-session"},
			)
			resp, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			scanner := bufio.NewScanner(resp.Body)
			require.True(t, scanner.Scan())
			require.Contains(t, scanner.Text(), "historical")
			// When: the stream is quiet past the ordinary response deadline.
			timer := time.NewTimer(200 * time.Millisecond)
			defer timer.Stop()
			<-timer.C
			f.broker.Broadcast(route.id, "after-timeout")
			// Then: a new event still reaches the client.
			for scanner.Scan() {
				if strings.Contains(scanner.Text(), "after-timeout") {
					break
				}
			}
			require.NoError(t, scanner.Err())
			require.Contains(t, scanner.Text(), "after-timeout")
			require.NoError(t, resp.Body.Close())
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("stream handler did not exit after client disconnect")
			}
		})
	}
}

type flushFailureWriter struct {
	http.ResponseWriter
	remaining int
}

func (w flushFailureWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *flushFailureWriter) FlushError() error {
	if w.remaining > 0 {
		w.remaining--
		return httpstream.Flush(w.ResponseWriter)
	}
	return errors.New("transport flush failed")
}

func TestHTTPStreamsExitOnFlushFailure(t *testing.T) {
	f := newStreamFixture(t)
	for _, route := range f.paths {
		for _, live := range []bool{false, true} {
			t.Run(
				fmt.Sprintf("%s/live=%t", route.path, live),
				func(t *testing.T) {
					// Given: the full middleware stack above a failing transport.
					done := make(chan struct{})
					srv := httptest.NewUnstartedServer(http.HandlerFunc(
						func(w http.ResponseWriter, r *http.Request) {
							defer close(done)
							transport := &flushFailureWriter{ResponseWriter: w}
							if live {
								transport.remaining = 1
							}
							f.router.ServeHTTP(transport, r)
						},
					))
					srv.Config = newHTTPServer("", srv.Config.Handler)
					srv.Start()
					defer srv.Close()
					defer srv.CloseClientConnections()
					req, err := http.NewRequest("GET", srv.URL+route.path, nil)
					require.NoError(t, err)
					req.Header.Set("Authorization", "Bearer ddp_pat_timeout")
					req.AddCookie(
						&http.Cookie{Name: "session", Value: "timeout-session"},
					)
					srv.Client().Timeout = 3 * time.Second
					// When: the historical or live event's flush fails.
					resp, err := srv.Client().Do(req)
					require.NoError(t, err)
					defer resp.Body.Close()
					require.Equal(t, http.StatusOK, resp.StatusCode)
					if live {
						timer := time.NewTimer(200 * time.Millisecond)
						defer timer.Stop()
						<-timer.C
						f.broker.Broadcast(route.id, "flush-fails")
					}
					// Then: the handler exits and its deferred unsubscribe runs.
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Fatal(
							"stream handler ignored a transport flush error",
						)
					}
				},
			)
		}
	}
}
