package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"durpdeploy/internal/deploymentstate"
	"durpdeploy/internal/runner"

	"github.com/go-chi/chi/v5"
)

func TestParentLogConflict_HTMLRejectsBeforeCursorParsing(t *testing.T) {
	// Given
	repo := setupTestRepo(t)
	deployment := durableLogDeployment(t, repo)
	_, err := repo.DB.Exec(
		`INSERT INTO agent_labels(id,name,normalized_name) VALUES(1,'label','label');
INSERT INTO deployment_routing_snapshots(deployment_id,source,target_mode,agent_label_id,agent_label_name,agent_strategy) VALUES(?,'request','label',1,'label','all')`,
		deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	logs := NewLogHandler(runner.NewLogBroker(), repo)
	for _, serve := range []http.HandlerFunc{logs.StreamLogs, logs.ExportLogs} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		request := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
		route := chi.NewRouteContext()
		route.URLParams.Add("id", fmt.Sprint(deployment.ID))
		request = request.WithContext(
			context.WithValue(request.Context(), chi.RouteCtxKey, route),
		)
		request.Header.Set("Last-Event-ID", "malformed")
		recorder := httptest.NewRecorder()
		// When
		serve(recorder, request)
		cancel()
		// Then
		if recorder.Code != 409 ||
			recorder.Header().
				Get("Content-Type") !=
				"text/plain; charset=utf-8" ||
			recorder.Body.String() != deploymentstate.ParentLogMessage+"\n" {
			t.Fatalf(
				"response=%d %v %s",
				recorder.Code,
				recorder.Header(),
				recorder.Body,
			)
		}
	}
}
