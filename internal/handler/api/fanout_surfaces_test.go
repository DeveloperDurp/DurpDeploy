package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/handler/api"
	"durpdeploy/internal/secret"
)

func fanoutSurfaceFixture(
	t *testing.T,
) (*harness, db.Deployment, []db.Deployment) {
	t.Helper()
	h := newAPIHarness(t)
	project := seedProject(t, h.repo)
	environment := seedEnv(t, h.repo)
	release := seedRelease(t, h.repo, project.ID)
	label, err := h.repo.Queries.CreateAgentLabel(
		context.Background(),
		db.CreateAgentLabelParams{
			Name:           "Frozen label",
			NormalizedName: "frozen label",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"agent-c", "agent-a", "agent-b"} {
		createRetryEligibleAgent(t, h, label.ID, id)
	}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	req := withAPIURLParam(httptest.NewRequest(
		http.MethodPost,
		"/",
		strings.NewReader(
			fmt.Sprintf(
				`{"release_id":%d,"environment_id":%d,"target_mode":"label","agent_label_id":%d,"agent_strategy":"all"}`,
				release.ID,
				environment.ID,
				label.ID,
			),
		),
	), "id", fmt.Sprint(project.ID))
	rec := httptest.NewRecorder()
	api.NewDeploymentHandler(h.repo, nil, dispatch.New(h.repo, box, nil)).
		CreateDeployment(rec, req)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var root db.Deployment
	mustDecode(t, rec.Body, &root)
	children, err := h.repo.Queries.ListDeploymentChildren(
		context.Background(),
		sql.NullInt64{Int64: root.ID, Valid: true},
	)
	if err != nil || len(children) != 3 {
		t.Fatalf("children: %v %v", children, err)
	}
	return h, root, children
}

func TestDeploymentFanoutAPI_OrderedCopiedIdentity(t *testing.T) {
	// Given
	h, root, children := fanoutSurfaceFixture(t)
	if _, err := h.repo.DB.Exec(`UPDATE agents SET name='renamed' WHERE id='agent-a'`); err != nil {
		t.Fatal(err)
	}
	deleteRequest := withAPIURLParam(
		httptest.NewRequest("DELETE", "/", nil),
		"agentID",
		"agent-b",
	)
	deleted := httptest.NewRecorder()
	handler.NewAgentAdminHandler(h.repo).DeleteAgent(deleted, deleteRequest)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body)
	}
	rec := httptest.NewRecorder()
	req := withAPIURLParam(
		httptest.NewRequest("GET", "/", nil),
		"id",
		fmt.Sprint(root.ID),
	)
	// When
	api.NewDeploymentHandler(h.repo, nil).GetDeployment(rec, req)
	// Then
	if rec.Code != 200 {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body)
	}
	var response struct {
		Dispatch struct {
			Mode       string `json:"mode"`
			Source     string `json:"source"`
			TargetMode string `json:"target_mode"`
			LabelName  string `json:"agent_label_name"`
			Strategy   string `json:"agent_strategy"`
			Aggregate  string `json:"aggregate_status"`
			Children   []struct {
				ID       int64  `json:"id"`
				Status   string `json:"status"`
				Dispatch struct {
					Agent struct{ ID, Name, Status string } `json:"agent"`
				} `json:"dispatch"`
			} `json:"children"`
		} `json:"dispatch"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	d := response.Dispatch
	if d.Mode != "fanout" || d.Source != "request" || d.TargetMode != "label" ||
		d.LabelName != "Frozen label" ||
		d.Strategy != "all" ||
		d.Aggregate != "pending" ||
		len(d.Children) != 3 {
		t.Fatalf("routing: %s", rec.Body)
	}
	for i, child := range d.Children {
		if child.ID != children[i].ID ||
			child.Dispatch.Agent.Name != children[i].TargetAgentName.String ||
			child.Dispatch.Agent.ID != children[i].TargetAgentID.String {
			t.Fatalf("child %d: %#v", i, child)
		}
	}
	if d.Children[1].Dispatch.Agent.Status != "deleted" {
		t.Fatal("deleted identity lost")
	}
}

func TestParentLogConflict_AllFormats(t *testing.T) {
	// Given
	h, root, children := fanoutSurfaceFixture(t)
	apiLogs := api.NewLogHandler(h.broker, h.repo)
	htmlLogs := handler.NewLogHandler(h.broker, h.repo)
	for _, test := range []struct {
		name, suffix, contentType string
		serve                     http.HandlerFunc
	}{
		{"json", "logs", "application/json", api.NewDeploymentHandler(h.repo, nil).ListDeploymentLogs},
		{"single", "logs/1", "application/json", apiLogs.GetLog},
		{"sse", "logs/stream", "application/json", apiLogs.StreamLogs},
		{"ndjson", "logs/stream?format=ndjson", "application/json", apiLogs.StreamLogs},
		{"text", "logs.txt", "application/json", apiLogs.ExportLogs},
		{"html-sse", "logs/stream", "text/plain; charset=utf-8", htmlLogs.StreamLogs},
		{"html-text", "logs.txt", "text/plain; charset=utf-8", htmlLogs.ExportLogs},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(
				context.Background(),
				100*time.Millisecond,
			)
			defer cancel()
			req := httptest.NewRequest("GET", fmt.Sprintf("/deployments/%d/%s", root.ID, test.suffix), nil).
				WithContext(ctx)
			req = withAPIURLParam(req, "id", fmt.Sprint(root.ID))
			req = withAPIURLParam(req, "logId", "1")
			rec := httptest.NewRecorder()
			// When
			test.serve(rec, req)
			// Then
			if rec.Code != 409 ||
				rec.Header().Get("Content-Type") != test.contentType ||
				rec.Header().Get("Location") != "" {
				t.Fatalf("response: %d %v %s", rec.Code, rec.Header(), rec.Body)
			}
			if test.contentType == "application/json" {
				var result struct {
					Code     string `json:"code"`
					Children []struct {
						ID      int64  `json:"id"`
						LogsURL string `json:"logs_url"`
					} `json:"children"`
				}
				mustDecode(t, rec.Body, &result)
				if result.Code != "fanout_parent_has_no_logs" ||
					len(result.Children) != 3 {
					t.Fatalf("conflict: %#v", result)
				}
				for i, child := range result.Children {
					if child.ID != children[i].ID ||
						child.LogsURL != fmt.Sprintf(
							"/api/v1/deployments/%d/logs",
							child.ID,
						) {
						t.Fatalf("child link: %#v", child)
					}
				}
			} else if rec.Body.String() != "Fan-out parents have no logs. Open a child deployment to view its logs.\n" {
				t.Fatalf("message: %s", rec.Body)
			}
		})
	}
}
