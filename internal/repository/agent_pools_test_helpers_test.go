package repository_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
)

func createAgentPool(
	t *testing.T,
	ctx context.Context,
	conn *sql.DB,
	name string,
) int64 {
	t.Helper()
	var id int64
	if err := conn.QueryRowContext(
		ctx, agentPoolQuery(t, "CreateAgentPool"), name, nil,
	).Scan(&id, new(string), new(sql.NullString), new(int64), new(int64), new(int64)); err != nil {
		t.Fatalf("CreateAgentPool(%q): %v", name, err)
	}
	return id
}

func createActiveAgent(
	t *testing.T,
	ctx context.Context,
	conn *sql.DB,
	id string,
) string {
	t.Helper()
	_, err := conn.ExecContext(ctx, `
INSERT INTO agents (id, name, status, certificate_pem, certificate_fingerprint)
VALUES (?, ?, 'active', 'certificate', ?)
`, id, id, strings.Repeat("a", 64-len(id))+id)
	if err != nil {
		t.Fatalf("create active agent %q: %v", id, err)
	}
	return id
}

func createEnvironment(
	t *testing.T,
	ctx context.Context,
	conn *sql.DB,
	name string,
) int64 {
	t.Helper()
	result, err := conn.ExecContext(
		ctx,
		"INSERT INTO environments (name) VALUES (?)",
		name,
	)
	if err != nil {
		t.Fatalf("create environment %q: %v", name, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("environment id: %v", err)
	}
	return id
}

func createProject(
	t *testing.T,
	ctx context.Context,
	conn *sql.DB,
	name string,
) int64 {
	t.Helper()
	result, err := conn.ExecContext(
		ctx,
		"INSERT INTO projects (name) VALUES (?)",
		name,
	)
	if err != nil {
		t.Fatalf("create project %q: %v", name, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("project id: %v", err)
	}
	return id
}

type deploymentTarget struct {
	projectID     int64
	environmentID int64
}

func createDeploymentFor(
	t *testing.T,
	ctx context.Context,
	conn *sql.DB,
	target deploymentTarget,
) {
	t.Helper()
	result, err := conn.ExecContext(
		ctx,
		"INSERT INTO releases (project_id, version, steps_json) VALUES (?, ?, '[]')",
		target.projectID,
		"selector-test",
	)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	releaseID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("release id: %v", err)
	}
	if _, err := conn.ExecContext(
		ctx,
		"INSERT INTO deployments (release_id, environment_id, status) VALUES (?, ?, 'pending')",
		releaseID,
		target.environmentID,
	); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
}

func addAgentToPool(
	t *testing.T,
	ctx context.Context,
	conn *sql.DB,
	poolID int64,
	agentID string,
) {
	t.Helper()
	if _, err := conn.ExecContext(
		ctx,
		agentPoolQuery(t, "AddAgentToPool"),
		poolID,
		agentID,
	); err != nil {
		t.Fatalf("AddAgentToPool(%d, %q): %v", poolID, agentID, err)
	}
}

func setAgentTag(
	t *testing.T,
	ctx context.Context,
	conn *sql.DB,
	agentID, key, value string,
) {
	t.Helper()
	if _, err := conn.ExecContext(
		ctx,
		agentPoolQuery(t, "SetAgentTag"),
		agentID,
		key,
		value,
	); err != nil {
		t.Fatalf("SetAgentTag(%q, %q): %v", agentID, key, err)
	}
}

func upsertEnvironmentPolicy(
	t *testing.T,
	ctx context.Context,
	conn *sql.DB,
	environmentID, poolID int64,
	selector string,
) {
	t.Helper()
	if _, err := conn.ExecContext(
		ctx,
		agentPoolQuery(t, "UpsertEnvironmentAgentPolicy"),
		environmentID,
		poolID,
		selector,
	); err != nil {
		t.Fatalf("UpsertEnvironmentAgentPolicy(%d): %v", environmentID, err)
	}
}

func matchingCandidates(
	t *testing.T,
	ctx context.Context,
	conn *sql.DB,
	environmentID int64,
	required map[string]string,
) []string {
	t.Helper()
	candidates := candidateIDs(t, ctx, conn, environmentID)
	matches := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		rows, err := conn.QueryContext(
			ctx,
			agentPoolQuery(t, "ListAgentTagsByAgent"),
			candidate,
		)
		if err != nil {
			t.Fatalf("ListAgentTagsByAgent(%q): %v", candidate, err)
		}
		tags := make(map[string]string)
		for rows.Next() {
			var key, value string
			if err := rows.Scan(&key, &value, new(int64)); err != nil {
				rows.Close()
				t.Fatalf("scan agent tag: %v", err)
			}
			tags[key] = value
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close agent tags: %v", err)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate agent tags: %v", err)
		}
		if tagSuperset(tags, required) {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func candidateIDs(
	t *testing.T,
	ctx context.Context,
	conn *sql.DB,
	environmentID int64,
) []string {
	t.Helper()
	rows, err := conn.QueryContext(
		ctx,
		agentPoolQuery(t, "ListAgentPoolCandidatesByEnvironment"),
		environmentID,
	)
	if err != nil {
		t.Fatalf(
			"ListAgentPoolCandidatesByEnvironment(%d): %v",
			environmentID,
			err,
		)
	}
	defer rows.Close()
	var candidates []string
	for rows.Next() {
		var id string
		if err := rows.Scan(
			&id,
			new(string),
			new(string),
			new(sql.NullString),
			new(sql.NullString),
			new(sql.NullString),
			new(sql.NullInt64),
			new(sql.NullInt64),
			new(sql.NullInt64),
			new(int64),
			new(int64),
		); err != nil {
			t.Fatalf("scan candidate: %v", err)
		}
		candidates = append(candidates, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate candidates: %v", err)
	}
	return candidates
}

func tagSuperset(tags, required map[string]string) bool {
	for key, value := range required {
		if tags[key] != value {
			return false
		}
	}
	return true
}

func sameStrings(got, want []string) bool {
	return strings.Join(got, "\x00") == strings.Join(want, "\x00")
}

func agentPoolQuery(t *testing.T, name string) string {
	t.Helper()
	contents, err := os.ReadFile("../../queries/agent_pools.sql")
	if err != nil {
		t.Fatalf("read agent pool queries: %v", err)
	}
	marker := "-- name: " + name + " "
	start := strings.Index(string(contents), marker)
	if start < 0 {
		t.Fatalf("query %q not found", name)
	}
	queryStart := strings.Index(string(contents[start:]), "\n") + start + 1
	next := strings.Index(string(contents[queryStart:]), "\n-- name: ")
	if next < 0 {
		return strings.TrimSpace(string(contents[queryStart:]))
	}
	return strings.TrimSpace(string(contents[queryStart : queryStart+next]))
}
