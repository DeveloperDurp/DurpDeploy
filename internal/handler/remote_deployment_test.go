package handler_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/handler/api"

	"github.com/go-chi/chi/v5"
)

func TestDeploymentList_RendersLostRemoteRoutingWhenFiltered(t *testing.T) {
	// Given
	harness := newHarness(t)
	deployment := seedRemoteDeployment(t, harness, "lost", "failed")
	seedLocalDeployment(t, harness)

	// When
	response, err := harness.authedClient().Get(fmt.Sprintf(
		"%s/deployments?remote_state=lost",
		harness.server.URL,
	))
	if err != nil {
		t.Fatalf("get deployments: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read deployments: %v", err)
	}

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if !strings.Contains(string(body), ">lost</span>") {
		t.Fatalf("lost routing state missing from deployment list: %s", body)
	}
	if strings.Contains(string(body), protocolSecretSentinel) {
		t.Fatal("deployment list rendered protocol secret data")
	}
	if !strings.Contains(string(body), fmt.Sprintf(
		"/deployments/%d", deployment.ID,
	)) {
		t.Fatal("filtered deployment missing from deployment list")
	}
}

func TestDeploymentDetail_RendersExplicitRedeployForUncertainRemoteTerminal(
	t *testing.T,
) {
	// Given
	harness := newHarness(t)
	deployment := seedRemoteDeployment(
		t,
		harness,
		"cancel_unconfirmed",
		"running",
	)

	// When
	response, err := harness.authedClient().Get(fmt.Sprintf(
		"%s/deployments/%d",
		harness.server.URL,
		deployment.ID,
	))
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read deployment: %v", err)
	}

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if !strings.Contains(string(body), "Cancellation was not confirmed") {
		t.Fatalf("uncertain remote terminal warning missing: %s", body)
	}
	if !strings.Contains(string(body), ">Redeploy<") {
		t.Fatal("explicit redeploy action missing for uncertain terminal state")
	}
	if strings.Contains(string(body), protocolSecretSentinel) {
		t.Fatal("deployment detail rendered protocol secret data")
	}
}

func TestRedeployDeployment_CreatesNewDeploymentWhenRemoteCancellationUnconfirmed(
	t *testing.T,
) {
	// Given
	harness := newHarness(t)
	source := seedRemoteDeployment(t, harness, "cancel_unconfirmed", "running")
	form := url.Values{"csrf_token": {harness.csrfToken()}}

	// When
	response, err := harness.authedClient().PostForm(
		fmt.Sprintf("%s/deployments/%d/redeploy", harness.server.URL, source.ID),
		form,
	)
	if err != nil {
		t.Fatalf("redeploy: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", response.StatusCode)
	}
	updated, err := harness.repo.Queries.GetDeployment(
		context.Background(),
		source.ID,
	)
	if err != nil {
		t.Fatalf("get source deployment: %v", err)
	}
	if updated.Status != "running" {
		t.Fatalf("source status = %q, want running", updated.Status)
	}
	deployments, err := harness.repo.Queries.ListDeploymentsByRelease(
		context.Background(),
		source.ReleaseID,
	)
	if err != nil {
		t.Fatalf("list redeployments: %v", err)
	}
	if len(deployments) != 2 {
		t.Fatalf("deployment count = %d, want 2", len(deployments))
	}
}

func TestAPIRedeployDeployment_CreatesNewDeploymentWhenRemoteCancellationUnconfirmed(
	t *testing.T,
) {
	// Given
	harness := newHarness(t)
	source := seedRemoteDeployment(t, harness, "cancel_unconfirmed", "running")
	router := chi.NewRouter()
	router.Post(
		"/deployments/{id}/redeploy",
		api.NewDeploymentHandler(harness.repo, harness.rnr).RedeployDeployment,
	)
	recorder := httptest.NewRecorder()

	// When
	router.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodPost,
			fmt.Sprintf("/deployments/%d/redeploy", source.ID),
			nil,
		),
	)

	// Then
	if recorder.Code != http.StatusCreated {
		t.Fatalf(
			"status = %d, want 201: %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	updated, err := harness.repo.Queries.GetDeployment(
		context.Background(),
		source.ID,
	)
	if err != nil {
		t.Fatalf("get source deployment: %v", err)
	}
	if updated.Status != "running" {
		t.Fatalf("source status = %q, want running", updated.Status)
	}
}

func TestDeploymentStatusAPI_IncludesSafeRemoteRouting(t *testing.T) {
	// Given
	harness := newHarness(t)
	deployment := seedRemoteDeployment(t, harness, "lost", "failed")
	router := chi.NewRouter()
	router.Get(
		"/deployments/{id}/status",
		api.NewDeploymentHandler(harness.repo, harness.rnr).GetDeploymentStatus,
	)
	recorder := httptest.NewRecorder()

	// When
	router.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			fmt.Sprintf("/deployments/%d/status", deployment.ID),
			nil,
		),
	)

	// Then
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, want 200: %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	if !strings.Contains(recorder.Body.String(), `"status":"failed"`) ||
		!strings.Contains(recorder.Body.String(), `"state":"lost"`) {
		t.Fatalf(
			"safe deployment and dispatch status missing: %s",
			recorder.Body.String(),
		)
	}
	if !strings.Contains(
		recorder.Body.String(),
		`"agent":{"id":"agent-lost","name":"Remote agent","status":"active","last_heartbeat_at":100}`,
	) {
		t.Fatalf(
			"agent health missing from deployment status: %s",
			recorder.Body.String(),
		)
	}
	if strings.Contains(recorder.Body.String(), protocolSecretSentinel) ||
		strings.Contains(recorder.Body.String(), "claim_token_hash") {
		t.Fatal("deployment status API exposed protocol secret data")
	}
}

func TestDeploymentListAPI_FiltersByRemoteStateWithoutSecrets(t *testing.T) {
	// Given
	harness := newHarness(t)
	deployment := seedRemoteDeployment(t, harness, "lost", "failed")
	seedLocalDeployment(t, harness)
	router := chi.NewRouter()
	router.Get(
		"/deployments",
		api.NewDeploymentHandler(harness.repo, harness.rnr).ListDeployments,
	)
	recorder := httptest.NewRecorder()

	// When
	router.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/deployments?remote_state=lost",
			nil,
		),
	)

	// Then
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, want 200: %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	if !strings.Contains(recorder.Body.String(), `"state":"lost"`) ||
		!strings.Contains(
			recorder.Body.String(),
			fmt.Sprintf(`"id":%d`, deployment.ID),
		) {
		t.Fatalf(
			"lost deployment missing from API list: %s",
			recorder.Body.String(),
		)
	}
	if strings.Contains(recorder.Body.String(), protocolSecretSentinel) ||
		strings.Contains(recorder.Body.String(), "claim_token_hash") {
		t.Fatal("deployment list API exposed protocol secret data")
	}
}

func TestRetryDeployment_RejectsLostRemoteDeployment(t *testing.T) {
	// Given
	harness := newHarness(t)
	deployment := seedRemoteDeployment(t, harness, "lost", "failed")
	router := chi.NewRouter()
	router.Post(
		"/deployments/{id}/retry",
		api.NewDeploymentHandler(harness.repo, harness.rnr).RetryDeployment,
	)
	recorder := httptest.NewRecorder()

	// When
	router.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodPost,
			fmt.Sprintf("/deployments/%d/retry", deployment.ID),
			nil,
		),
	)

	// Then
	if recorder.Code != http.StatusConflict {
		t.Fatalf(
			"status = %d, want 409: %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	updated, err := harness.repo.Queries.GetDeployment(
		context.Background(),
		deployment.ID,
	)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if updated.Status != "failed" {
		t.Fatalf("deployment status = %q, want failed", updated.Status)
	}
}
