package handler_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestEnvironmentPolicy_UpdateRollsBackWhenPoolIsDisabled(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	env := h.makeEnv("unchanged")
	pool := h.makePool("will-disable")
	if err := h.repo.Queries.UpsertEnvironmentAgentPolicy(
		context.Background(),
		db.UpsertEnvironmentAgentPolicyParams{
			EnvironmentID: env.ID, PoolID: pool.ID, Selector: "role=web",
		},
	); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	if err := h.repo.Queries.DisableAgentPool(
		context.Background(),
		pool.ID,
	); err != nil {
		t.Fatalf("disable pool: %v", err)
	}
	form := url.Values{
		"name":           {"changed"},
		"execution_mode": {"remote"},
		"pool_id":        {strconv.FormatInt(pool.ID, 10)},
		"agent_tags":     {"role=api"},
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
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf(
			"status = %d, want %d",
			resp.StatusCode,
			http.StatusUnprocessableEntity,
		)
	}
	gotEnvironment, err := h.repo.Queries.GetEnvironment(
		context.Background(),
		env.ID,
	)
	if err != nil {
		t.Fatalf("get environment: %v", err)
	}
	if gotEnvironment.Name != "unchanged" {
		t.Fatalf("environment name = %q, want unchanged", gotEnvironment.Name)
	}
	policy, err := h.repo.Queries.GetEnvironmentAgentPolicy(
		context.Background(), env.ID,
	)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if policy.Selector != "role=web" {
		t.Fatalf("policy selector = %q, want role=web", policy.Selector)
	}
}

func TestEnvironmentPolicy_EditFormRendersRemoteSelection(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	env := h.makeEnv("remote-edit")
	pool := h.makePool("edit-pool")
	if err := h.repo.Queries.UpsertEnvironmentAgentPolicy(
		context.Background(),
		db.UpsertEnvironmentAgentPolicyParams{
			EnvironmentID: env.ID, PoolID: pool.ID, Selector: "region=us,role=web",
		},
	); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	// When
	resp, err := h.authedClient().Get(
		h.server.URL + "/environments/" + strconv.FormatInt(env.ID, 10) +
			"/edit",
	)
	if err != nil {
		t.Fatalf("get edit environment form: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	// Then
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(body, `value="remote" checked`) ||
		!strings.Contains(
			body,
			`value="`+strconv.FormatInt(pool.ID, 10)+`" selected`,
		) ||
		!strings.Contains(body, `value="region=us,role=web"`) {
		t.Fatalf("edit form is missing its configured remote policy: %s", body)
	}
}

func TestEnvironmentPolicy_CreateRequiresCSRFAndWriterRole(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	pool := h.makePool("protected")
	form := url.Values{
		"name":           {"protected"},
		"execution_mode": {"remote"},
		"pool_id":        {strconv.FormatInt(pool.ID, 10)},
	}

	// When
	withoutCSRF := h.submitEnvironment(http.MethodPost, "/environments", form)
	defer withoutCSRF.Body.Close()
	h.setRole("viewer")
	form.Set("csrf_token", h.csrfToken())
	viewer := h.submitEnvironment(http.MethodPost, "/environments", form)
	defer viewer.Body.Close()

	// Then
	if withoutCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"missing CSRF status = %d, want %d",
			withoutCSRF.StatusCode,
			http.StatusForbidden,
		)
	}
	if viewer.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"viewer status = %d, want %d",
			viewer.StatusCode,
			http.StatusForbidden,
		)
	}
	count, err := h.repo.Queries.CountEnvironments(context.Background())
	if err != nil || count != 0 {
		t.Fatalf("environment count = %d, %v; want zero", count, err)
	}
}

func TestEnvironmentPolicy_CreateDefaultsToLocal(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	form := url.Values{
		"name":       {"legacy-local"},
		"csrf_token": {h.csrfToken()},
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
	if _, err := h.repo.Queries.GetEnvironmentAgentPolicy(
		context.Background(), envs[0].ID,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get local environment policy error = %v, want no rows", err)
	}
}

func TestEnvironmentPolicy_CreateConflictPreservesExistingPolicy(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	env := h.makeEnv("conflict")
	pool := h.makePool("existing-policy")
	if err := h.repo.Queries.UpsertEnvironmentAgentPolicy(
		context.Background(),
		db.UpsertEnvironmentAgentPolicyParams{
			EnvironmentID: env.ID, PoolID: pool.ID, Selector: "role=web",
		},
	); err != nil {
		t.Fatalf("create existing policy: %v", err)
	}
	form := url.Values{
		"name":           {env.Name},
		"execution_mode": {"remote"},
		"pool_id":        {strconv.FormatInt(pool.ID, 10)},
		"agent_tags":     {"role=api"},
		"csrf_token":     {h.csrfToken()},
	}

	// When
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
	policy, err := h.repo.Queries.GetEnvironmentAgentPolicy(
		context.Background(), env.ID,
	)
	if err != nil {
		t.Fatalf("get existing policy: %v", err)
	}
	if policy.Selector != "role=web" {
		t.Fatalf("policy selector = %q, want role=web", policy.Selector)
	}
}
