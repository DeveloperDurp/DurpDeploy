package handler_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func (h *projectHarness) submitEnvironment(
	method string,
	path string,
	form url.Values,
) *http.Response {
	h.t.Helper()
	req, err := http.NewRequest(
		method,
		h.server.URL+path,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		h.t.Fatalf("new environment request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.authedClient().Do(req)
	if err != nil {
		h.t.Fatalf("submit environment request: %v", err)
	}
	return resp
}

func (h *projectHarness) makePool(name string) db.AgentPool {
	h.t.Helper()
	pool, err := h.repo.Queries.CreateAgentPool(
		context.Background(),
		db.CreateAgentPoolParams{Name: name, Description: sql.NullString{}},
	)
	if err != nil {
		h.t.Fatalf("create pool: %v", err)
	}
	return pool
}

func TestEnvironmentPolicy_NewFormDefaultsToLocal(t *testing.T) {
	// Given
	h := newProjectHarness(t)

	// When
	resp, err := h.authedClient().Get(h.server.URL + "/environments/new")
	if err != nil {
		t.Fatalf("get new environment form: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	// Then
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(body, `name="execution_mode"`) ||
		!strings.Contains(body, `value="local" checked`) ||
		!strings.Contains(body, "never fall back to the local runner") {
		t.Fatalf("new form is missing the local policy controls: %s", body)
	}
}

func TestEnvironmentPolicy_CreateRemoteCanonicalizesTags(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	pool := h.makePool("remote-create")
	form := url.Values{
		"name":           {"remote"},
		"execution_mode": {"remote"},
		"pool_id":        {strconv.FormatInt(pool.ID, 10)},
		"agent_tags":     {"role=web, region=us"},
		"csrf_token":     {h.csrfToken()},
	}

	// When
	resp := h.submitEnvironment(http.MethodPost, "/environments", form)
	defer resp.Body.Close()

	// Then
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	envs, err := h.repo.Queries.ListEnvironments(context.Background())
	if err != nil || len(envs) != 1 {
		t.Fatalf("list environments = %v, %v; want one environment", envs, err)
	}
	policy, err := h.repo.Queries.GetEnvironmentAgentPolicy(
		context.Background(), envs[0].ID,
	)
	if err != nil {
		t.Fatalf("get environment policy: %v", err)
	}
	if policy.PoolID != pool.ID || policy.Selector != "region=us,role=web" {
		t.Fatalf("policy = %#v, want canonical remote policy", policy)
	}
}

func TestEnvironmentPolicy_CreateRejectsInvalidRemoteInput(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	disabledPool := h.makePool("disabled")
	if err := h.repo.Queries.DisableAgentPool(
		context.Background(),
		disabledPool.ID,
	); err != nil {
		t.Fatalf("disable pool: %v", err)
	}
	cases := []struct {
		name string
		form url.Values
	}{
		{"missing pool", url.Values{"agent_tags": {"role=web"}}},
		{
			"unknown pool",
			url.Values{"pool_id": {"999"}, "agent_tags": {"role=web"}},
		},
		{
			"disabled pool",
			url.Values{"pool_id": {strconv.FormatInt(disabledPool.ID, 10)}},
		},
		{
			"duplicate tags",
			url.Values{"pool_id": {"1"}, "agent_tags": {"role=web,role=api"}},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// When
			form := test.form
			form.Set("name", "invalid-"+strings.ReplaceAll(test.name, " ", "-"))
			form.Set("execution_mode", "remote")
			form.Set("csrf_token", h.csrfToken())
			resp := h.submitEnvironment(http.MethodPost, "/environments", form)
			defer resp.Body.Close()

			// Then
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf(
					"status = %d, want %d",
					resp.StatusCode,
					http.StatusUnprocessableEntity,
				)
			}
			count, err := h.repo.Queries.CountEnvironments(context.Background())
			if err != nil || count != 0 {
				t.Fatalf("environment count = %d, %v; want zero", count, err)
			}
		})
	}
}

func TestEnvironmentPolicy_UpdateLocalDeletesPolicyWithoutChangingDispatch(
	t *testing.T,
) {
	// Given
	h := newProjectHarness(t)
	env := h.makeEnv("remote-before-local")
	pool := h.makePool("remote-update")
	if err := h.repo.Queries.UpsertEnvironmentAgentPolicy(
		context.Background(),
		db.UpsertEnvironmentAgentPolicyParams{
			EnvironmentID: env.ID, PoolID: pool.ID, Selector: "role=web",
		},
	); err != nil {
		t.Fatalf("create environment policy: %v", err)
	}
	project := h.makeProject("snapshot-project")
	release := h.makeRelease(project.ID, "v1", "true")
	deployment, err := h.repo.Queries.CreateDeployment(
		context.Background(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: env.ID, Status: "pending",
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	wantDispatch, err := h.repo.Queries.CreateDeploymentDispatch(
		context.Background(),
		db.CreateDeploymentDispatchParams{
			DeploymentID: deployment.ID,
			Mode:         "remote",
			PoolID:       sql.NullInt64{Int64: pool.ID, Valid: true},
			Selector:     "role=web",
			State:        "waiting",
		},
	)
	if err != nil {
		t.Fatalf("create dispatch snapshot: %v", err)
	}
	form := url.Values{
		"name":           {env.Name},
		"execution_mode": {"local"},
		"csrf_token":     {h.csrfToken()},
	}

	// When
	resp := h.submitEnvironment(
		http.MethodPut,
		"/environments/"+strconv.FormatInt(env.ID, 10),
		form,
	)
	defer resp.Body.Close()

	// Then
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	if _, err := h.repo.Queries.GetEnvironmentAgentPolicy(
		context.Background(), env.ID,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get deleted policy error = %v, want no rows", err)
	}
	gotDispatch, err := h.repo.Queries.GetDeploymentDispatch(
		context.Background(), deployment.ID,
	)
	if err != nil {
		t.Fatalf("get dispatch snapshot: %v", err)
	}
	if !reflect.DeepEqual(gotDispatch, wantDispatch) {
		t.Fatalf(
			"dispatch changed: got %#v, want %#v",
			gotDispatch,
			wantDispatch,
		)
	}
}
