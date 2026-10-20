//go:build e2e

package handler_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
	"durpdeploy/internal/testutil"
	"github.com/robfig/cron/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestLifecycleStageOrderAPIWebE2E(t *testing.T) {
	for _, backend := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(backend, func(t *testing.T) {
			conn, err := migrate.Run(stageOrderDSN(t, backend))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { testutil.Close(t, conn) })
			repo := repository.New(conn)
			rnr := runner.New(repo, runner.NewLogBroker())
			t.Cleanup(rnr.KillAll)
			srv := httptest.NewServer(server.NewRouter(
				repo,
				rnr,
				cron.NewParser(
					cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow,
				),
				handler.NewAuthHandler(repo),
			))
			t.Cleanup(srv.Close)
			h := &projectHarness{t: t, repo: repo, server: srv,
				sess: seedSessionAs(
					t,
					repo,
					srv.URL,
					"order@example.test",
					"admin",
				)}
			h.authedClient().Timeout = 30 * time.Second
			token := seedAPIToken(t, &authHarness{repo: repo}, h.sess.user.ID)
			request := func(method, path string, data map[string]any, web bool) *http.Request {
				t.Helper()
				body, err := json.Marshal(data)
				if err != nil {
					t.Fatal(err)
				}
				if data == nil {
					body = nil
				}
				if web {
					form := url.Values{"csrf_token": {h.csrfToken()}}
					for key, value := range data {
						form.Set(key, fmt.Sprint(value))
					}
					body = []byte(form.Encode())
				}
				req, err := http.NewRequestWithContext(t.Context(), method,
					srv.URL+path, bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+token)
				if web {
					req.Header.Set(
						"Content-Type",
						"application/x-www-form-urlencoded",
					)
					req.Header.Set("X-CSRF-Token", h.csrfToken())
				}
				return req
			}
			send := func(req *http.Request, want int) []byte {
				t.Helper()
				response := doStageOrderRequest(h.authedClient(), req)
				assertStageOrderResponse(t, response, want)
				return response.body
			}
			var lifecycle struct{ ID int64 }
			if err := json.Unmarshal(send(request("POST", "/api/v1/lifecycles",
				map[string]any{"name": "Stage ordering"}, false), 201), &lifecycle); err != nil {
				t.Fatal(err)
			}
			path := fmt.Sprintf("/lifecycles/%d", lifecycle.ID)
			api := "/api/v1" + path
			stages := func() []db.LifecycleStage {
				t.Helper()
				var saved struct{ Stages []db.LifecycleStage }
				if err := json.Unmarshal(send(request("GET", api, nil, false), 200), &saved); err != nil {
					t.Fatal(err)
				}
				return saved.Stages
			}
			add := func(name string, position *int64) db.LifecycleStage {
				t.Helper()
				env := h.makeEnv(name)
				data := map[string]any{
					"environment_id":    env.ID,
					"requires_approval": true,
				}
				if position != nil {
					data["sort_order"] = *position
				}
				var stage db.LifecycleStage
				if err := json.Unmarshal(send(request("POST", api+"/stages", data, false), 201), &stage); err != nil {
					t.Fatal(err)
				}
				if stage.RequiresApproval != 1 {
					t.Fatal("stage creation lost the approval flag")
				}
				return stage
			}
			for _, input := range []struct {
				name     string
				position *int64
				want     int64
			}{
				{"omitted", nil, 1},
				{"explicit", orderPosition(10), 10},
				{"gapped", orderPosition(5), 5},
				{"zero", orderPosition(0), 11},
				{"negative", orderPosition(-3), 12},
			} {
				if stage := add(input.name, input.position); stage.SortOrder != input.want {
					t.Fatalf(
						"%s: position=%d, want %d",
						input.name,
						stage.SortOrder,
						input.want,
					)
				}
			}
			before := stages()
			missing := fmt.Sprintf("/lifecycles/%d/stages", lifecycle.ID+1)
			payload := map[string]any{"environment_id": before[0].EnvironmentID}
			body := send(
				request("POST", "/api/v1"+missing, payload, false),
				404,
			)
			var failure struct{ Error string }
			if err := json.Unmarshal(body, &failure); err != nil ||
				failure.Error != "Lifecycle not found" {
				t.Fatalf("missing lifecycle response: %s, error=%v", body, err)
			}
			send(request("POST", missing, payload, true), 404)
			if !slices.Equal(before, stages()) {
				t.Fatal("missing lifecycle requests changed stages")
			}
			conflictEnv := h.makeEnv("position-conflict")
			for _, conflict := range []struct {
				data  map[string]any
				field string
			}{
				{map[string]any{"environment_id": conflictEnv.ID, "sort_order": 5}, "sort_order"},
				{map[string]any{"environment_id": before[0].EnvironmentID, "sort_order": 20}, "environment_id"},
			} {
				if body := send(request("POST", api+"/stages", conflict.data, false), 409); !strings.Contains(
					string(body),
					conflict.field,
				) {
					t.Fatalf(
						"conflict does not identify %s: %s",
						conflict.field,
						body,
					)
				}
			}
			send(
				request("PATCH", fmt.Sprintf("%s/stages/%d", api, before[0].ID),
					map[string]any{"sort_order": 5}, false),
				409,
			)
			if !slices.Equal(before, stages()) {
				t.Fatal("conflicting creation or update changed the stages")
			}
			webEnv := h.makeEnv("web-append")
			send(request("POST", path+"/stages",
				map[string]any{"environment_id": webEnv.ID}, true), 303)
			after := stages()
			if after[len(after)-1].SortOrder != 13 {
				t.Fatal("web stage did not append after the highest position")
			}
			send(request("POST", path+"/stages",
				map[string]any{"environment_id": webEnv.ID}, true), 400)
			if !slices.Equal(after, stages()) {
				t.Fatal("duplicate web environment changed stages")
			}

			// Every request enters through HTTP. Alternate transports while
			// releasing all append requests together against the same parent.
			start := make(chan struct{})
			results := make(chan stageOrderResponse, 8)
			for i := range 8 {
				env := h.makeEnv("concurrent-" + strconv.Itoa(i))
				web, target := i%2 == 0, api
				if web {
					target = path
				}
				req := request("POST", target+"/stages",
					map[string]any{"environment_id": env.ID}, web)
				go func() {
					<-start
					response := doStageOrderRequest(h.authedClient(), req)
					if web && response.status == 303 {
						response.status = 201
					}
					results <- response
				}()
			}
			close(start)
			for range 8 {
				assertStageOrderResponse(t, <-results, 201)
			}
			after = stages()
			if len(after) != 14 {
				t.Fatalf("concurrent append count=%d", len(after))
			}
			for i, stage := range after[6:] {
				if stage.SortOrder != int64(14+i) {
					t.Fatalf(
						"concurrent position=%d, want %d",
						stage.SortOrder,
						14+i,
					)
				}
			}

			// Both requests add the same environment. One must win and the
			// other must keep the existing conflict response without a write.
			duplicate := h.makeEnv("concurrent-duplicate")
			start = make(chan struct{})
			for range 2 {
				req := request("POST", api+"/stages",
					map[string]any{"environment_id": duplicate.ID}, false)
				go func() { <-start; results <- doStageOrderRequest(h.authedClient(), req) }()
			}
			close(start)
			statuses := []int{(<-results).status, (<-results).status}
			slices.Sort(statuses)
			if !slices.Equal(statuses, []int{201, 409}) || len(stages()) != 15 {
				t.Fatalf("duplicate append responses=%v", statuses)
			}
			before = stages()
			ids := make([]int64, len(before))
			for i := range before {
				ids[i] = before[len(before)-1-i].ID
			}
			send(request("POST", api+"/stages/reorder",
				map[string]any{"stage_ids": ids}, false), 200)
			for i, stage := range stages() {
				if stage.ID != ids[i] || stage.SortOrder != int64(i+1) {
					t.Fatal(
						"API reorder did not assign consecutive requested positions",
					)
				}
			}
			// Appending must not overflow after the largest explicit value.
			add("last-position", orderPosition(math.MaxInt64))
			before = stages()
			send(request("POST", api+"/stages",
				map[string]any{"environment_id": conflictEnv.ID}, false), 409)
			if !slices.Equal(before, stages()) {
				t.Fatal("exhausted append changed stages")
			}
		})
	}
}

func orderPosition(value int64) *int64 { return &value }

func stageOrderDSN(t *testing.T, backend string) string {
	t.Helper()
	switch backend {
	case "SQLite":
		return "file:" + filepath.Join(t.TempDir(), "stage-order.db") +
			"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)" +
			"&_pragma=busy_timeout(5000)"
	case "PostgreSQL":
		container, err := postgres.Run(
			t.Context(),
			"harbor.durp.info/dockerhub/library/postgres:16-alpine",
			postgres.WithDatabase("project"),
			postgres.WithUsername("project"),
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
	case "SQLServer":
		return testutil.SQLServerDSN(t)
	default:
		t.Fatalf("unknown backend %q", backend)
		return ""
	}
}

type stageOrderResponse struct {
	status int
	body   []byte
	err    error
}

func doStageOrderRequest(
	client *http.Client,
	request *http.Request,
) stageOrderResponse {
	response, err := client.Do(request)
	if err != nil {
		return stageOrderResponse{err: err}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return stageOrderResponse{
		status: response.StatusCode,
		body:   body,
		err:    err,
	}
}

func assertStageOrderResponse(
	t *testing.T,
	response stageOrderResponse,
	want int,
) {
	t.Helper()
	if response.err != nil || response.status != want {
		t.Fatalf(
			"response=%d, want=%d, error=%v, body=%s",
			response.status,
			want,
			response.err,
			response.body,
		)
	}
}
