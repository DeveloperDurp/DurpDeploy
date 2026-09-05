package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
)

func TestProjectExecutionPolicy_CreateDefaultsLocalAndUpdateLabel(
	t *testing.T,
) {
	// Given
	h := newHarness(t)
	admin := h.seedUser(t, "policy-admin@example.com", "admin")
	token := h.seedToken(t, admin)
	label, err := h.repo.Queries.CreateAgentLabel(
		context.Background(),
		db.CreateAgentLabelParams{Name: "Cat Fact", NormalizedName: "cat fact"},
	)
	if err != nil {
		t.Fatalf("create label: %v", err)
	}

	// When a project omits an execution policy at creation.
	created := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects",
		token,
		`{"name":"policy-project"}`,
	)
	h.assertStatus(t, created, http.StatusCreated)
	var createResponse map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &createResponse); err != nil {
		t.Fatalf("decode created project: %v", err)
	}
	projectID := int64(createResponse["id"].(float64))

	// Then the persisted policy is explicit local rather than legacy.
	stored, err := h.repo.Queries.GetProjectExecutionPolicy(
		context.Background(),
		projectID,
	)
	if err != nil {
		t.Fatalf("get default policy: %v", err)
	}
	if stored.TargetMode != "local" || stored.AgentLabelID.Valid ||
		stored.AgentStrategy.Valid {
		t.Fatalf("default policy = %#v, want explicit local", stored)
	}
	t.Logf(
		"SQLite policy after create: project=%d mode=%s label_valid=%t strategy_valid=%t",
		projectID,
		stored.TargetMode,
		stored.AgentLabelID.Valid,
		stored.AgentStrategy.Valid,
	)

	// When the project is updated to route the entire label.
	updated := h.request(
		t,
		http.MethodPut,
		"/api/v1/projects/"+itoa(projectID),
		token,
		`{"name":"policy-project","target_mode":"label",`+
			`"agent_label_id":`+itoa(label.ID)+`,"agent_strategy":"all"}`,
	)
	h.assertStatus(t, updated, http.StatusOK)

	// Then only the safe display name is exposed and the persisted policy
	// uses the requested all strategy.
	if strings.Contains(updated.Body.String(), "normalized_name") {
		t.Fatalf(
			"project response leaked label normalization: %s",
			updated.Body.String(),
		)
	}
	if !strings.Contains(updated.Body.String(), `"label_name":"Cat Fact"`) ||
		!strings.Contains(updated.Body.String(), `"agent_strategy":"all"`) {
		t.Fatalf(
			"project response omitted execution policy: %s",
			updated.Body.String(),
		)
	}
	stored, err = h.repo.Queries.GetProjectExecutionPolicy(
		context.Background(),
		projectID,
	)
	if err != nil {
		t.Fatalf("get updated policy: %v", err)
	}
	if stored.TargetMode != "label" || !stored.AgentLabelID.Valid ||
		stored.AgentLabelID.Int64 != label.ID || !stored.AgentStrategy.Valid ||
		stored.AgentStrategy.String != "all" {
		t.Fatalf("updated policy = %#v, want label/all", stored)
	}

	roundRobin := h.request(
		t,
		http.MethodPut,
		"/api/v1/projects/"+itoa(projectID),
		token,
		`{"name":"policy-project","target_mode":"label",`+
			`"agent_label_id":`+itoa(label.ID)+`,"agent_strategy":"round_robin"}`,
	)
	h.assertStatus(t, roundRobin, http.StatusOK)
	stored, err = h.repo.Queries.GetProjectExecutionPolicy(
		context.Background(),
		projectID,
	)
	if err != nil || stored.AgentStrategy.String != "round_robin" {
		t.Fatalf("round-robin policy = %#v, %v", stored, err)
	}
	t.Logf(
		"SQLite policy after round-robin update: project=%d mode=%s label=%d strategy=%s",
		projectID,
		stored.TargetMode,
		stored.AgentLabelID.Int64,
		stored.AgentStrategy.String,
	)
	entries, err := h.repo.Queries.ListAuditLogs(context.Background(), 10)
	if err != nil {
		t.Fatalf("list project policy audit: %v", err)
	}
	if len(entries) == 0 || entries[0].Action != "update_project" {
		t.Fatalf(
			"latest project policy audit = %#v, want update_project",
			entries,
		)
	}
}

func TestProjectExecutionPolicy_LegacyRequiresExplicitSelection(t *testing.T) {
	// Given a project created before execution policies existed.
	h := newHarness(t)
	admin := h.seedUser(t, "legacy-admin@example.com", "admin")
	token := h.seedToken(t, admin)
	project := h.seedProject(t, admin)

	// When it is saved without an execution target.
	rejected := h.request(
		t,
		http.MethodPut,
		"/api/v1/projects/"+itoa(project.ID),
		token,
		`{"name":"legacy-renamed"}`,
	)

	// Then it remains a legacy project and does not partially update.
	h.assertStatus(t, rejected, http.StatusUnprocessableEntity)
	if _, err := h.repo.Queries.GetProjectExecutionPolicy(
		context.Background(),
		project.ID,
	); err != sql.ErrNoRows {
		t.Fatalf("legacy project policy error = %v, want sql.ErrNoRows", err)
	}
	unchanged, err := h.repo.Queries.GetProject(
		context.Background(),
		project.ID,
	)
	if err != nil {
		t.Fatalf("get unchanged project: %v", err)
	}
	if unchanged.Name != project.Name {
		t.Fatalf("legacy update changed name to %q", unchanged.Name)
	}

	// When the same project explicitly selects local.
	accepted := h.request(
		t,
		http.MethodPut,
		"/api/v1/projects/"+itoa(project.ID),
		token,
		`{"name":"legacy-renamed","target_mode":"local"}`,
	)
	h.assertStatus(t, accepted, http.StatusOK)
	stored, err := h.repo.Queries.GetProjectExecutionPolicy(
		context.Background(),
		project.ID,
	)
	if err != nil || stored.TargetMode != "local" {
		t.Fatalf("explicit local policy = %#v, %v", stored, err)
	}
	environment := h.seedEnvironment(t, "legacy-policy-environment")
	_, err = h.repo.DB.ExecContext(context.Background(), `
		INSERT INTO agents (
			id, name, status, certificate_pem, certificate_fingerprint, last_heartbeat_at
		) VALUES ('legacy-agent', 'Legacy Agent', 'active', 'certificate', ?, 100)
	`, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("create legacy environment agent: %v", err)
	}
	_, err = h.repo.DB.ExecContext(
		context.Background(),
		"INSERT INTO environment_agent_assignments (environment_id, agent_id) VALUES (?, 'legacy-agent')",
		environment.ID,
	)
	if err != nil {
		t.Fatalf("assign legacy environment agent: %v", err)
	}
	resolved, err := dispatch.NewResolver(h.repo).Resolve(
		context.Background(),
		project.ID,
		environment.ID,
		dispatch.Input{Source: dispatch.SourceRequest, Mode: "default"},
	)
	if err != nil || resolved.Source != dispatch.SourceProject ||
		resolved.Mode != dispatch.TargetLocal {
		t.Fatalf(
			"saved policy should bypass assignment = %#v, %v",
			resolved,
			err,
		)
	}
	t.Logf(
		"stored local policy bypasses environment assignment: source=%s mode=%s",
		resolved.Source,
		resolved.Mode,
	)
}

func TestProjectExecutionPolicy_RejectsMissingStrategyAndUnknownLabel(
	t *testing.T,
) {
	// Given an existing project.
	h := newHarness(t)
	admin := h.seedUser(t, "validation-admin@example.com", "admin")
	token := h.seedToken(t, admin)
	project := h.seedProject(t, admin)

	// When a label policy is incomplete or names no label.
	for _, body := range []string{
		`{"name":"test-project","target_mode":"label","agent_label_id":1}`,
		`{"name":"test-project","target_mode":"label",` +
			`"agent_label_id":999,"agent_strategy":"round_robin"}`,
	} {
		rejected := h.request(
			t,
			http.MethodPut,
			"/api/v1/projects/"+itoa(project.ID),
			token,
			body,
		)
		if rejected.Code != http.StatusUnprocessableEntity &&
			rejected.Code != http.StatusNotFound {
			t.Fatalf(
				"status = %d, want 422 or 404: %s",
				rejected.Code,
				rejected.Body.String(),
			)
		}
	}

	// Then no policy row was written.
	if _, err := h.repo.Queries.GetProjectExecutionPolicy(
		context.Background(),
		project.ID,
	); err != sql.ErrNoRows {
		t.Fatalf("rejected update wrote policy: %v", err)
	}
}

func TestProjectExecutionPolicy_PreservesViewerAndProjectAccessGates(
	t *testing.T,
) {
	h := newHarness(t)
	admin := h.seedUser(t, "access-admin@example.com", "admin")
	adminToken := h.seedToken(t, admin)
	project := h.seedProject(t, admin)
	viewer := h.seedUser(t, "policy-viewer@example.com", "viewer")
	viewerToken := h.seedToken(t, viewer)
	nonmember := h.seedUser(t, "policy-nonmember@example.com", "deployer")
	nonmemberToken := h.seedToken(t, nonmember)
	body := `{"name":"test-project","target_mode":"local"}`

	viewerResponse := h.request(
		t,
		http.MethodPut,
		"/api/v1/projects/"+itoa(project.ID),
		viewerToken,
		body,
	)
	h.assertStatus(t, viewerResponse, http.StatusForbidden)
	nonmemberResponse := h.request(
		t,
		http.MethodPut,
		"/api/v1/projects/"+itoa(project.ID),
		nonmemberToken,
		body,
	)
	h.assertStatus(t, nonmemberResponse, http.StatusForbidden)
	if _, err := h.repo.Queries.GetProjectExecutionPolicy(
		context.Background(),
		project.ID,
	); err != sql.ErrNoRows {
		t.Fatalf("denied request wrote policy: %v", err)
	}

	allowed := h.request(
		t,
		http.MethodPut,
		"/api/v1/projects/"+itoa(project.ID),
		adminToken,
		body,
	)
	h.assertStatus(t, allowed, http.StatusOK)
}
