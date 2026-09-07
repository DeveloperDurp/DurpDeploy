package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/handler"
	"durpdeploy/internal/handler/api"
)

func TestDeploymentFanoutAPI_ChildActionsAlwaysConflict(t *testing.T) {
	// Given
	h, _, children := fanoutSurfaceFixture(t)
	apiHandler := api.NewDeploymentHandler(h.repo, nil)
	htmlHandler := handler.NewDeploymentHandler(h.repo, nil)
	for _, test := range []struct {
		name  string
		serve http.HandlerFunc
	}{
		{"api-cancel", apiHandler.CancelDeployment},
		{"api-retry", apiHandler.RetryDeployment},
		{"api-redeploy", apiHandler.RedeployDeployment},
		{"html-cancel", htmlHandler.CancelDeployment},
		{"html-redeploy", htmlHandler.RedeployDeployment},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := withAPIURLParam(
				httptest.NewRequest("POST", "/", nil),
				"id",
				fmt.Sprint(children[0].ID),
			)
			recorder := httptest.NewRecorder()
			// When
			test.serve(recorder, request)
			// Then
			if recorder.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
			}
		})
	}
}
