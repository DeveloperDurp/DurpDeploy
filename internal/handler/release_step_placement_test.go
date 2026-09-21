package handler

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestCreateReleaseSnapshotPreservesStepPlacement(t *testing.T) {
	conn, err := migrate.Run(filepath.Join(t.TempDir(), "placement.db") +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	repo := repository.New(conn)
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "placement"},
	)
	if err != nil {
		t.Fatal(err)
	}
	step, err := repo.CreateStepWithPlacement(
		t.Context(),
		db.CreateStepParams{
			ProjectID: project.ID, Name: "remote", ScriptBody: "echo remote",
			Interpreter: "python3",
		},
		"agent",
		[]string{"linux"},
	)
	if err != nil {
		t.Fatal(err)
	}

	release, err := CreateReleaseSnapshot(
		t.Context(), repo, project.ID, "v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	var snapshots []releaseStepSnapshot
	if err := json.Unmarshal(
		[]byte(release.StepsJson),
		&snapshots,
	); err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].ExecutionTarget != "agent" ||
		snapshots[0].Interpreter != "python3" ||
		len(snapshots[0].AgentSelectors) != 1 ||
		snapshots[0].AgentSelectors[0] != "linux" || step.ID == 0 {
		t.Fatalf("snapshot = %+v", snapshots)
	}
	if _, err := repo.Queries.UpdateStep(t.Context(), db.UpdateStepParams{
		ID: step.ID, Name: step.Name, ScriptBody: step.ScriptBody,
		SortOrder: step.SortOrder, TimeoutSeconds: step.TimeoutSeconds,
		MaxRetries: step.MaxRetries, Interpreter: "pwsh",
	}); err != nil {
		t.Fatal(err)
	}
	frozen, err := repo.Queries.GetRelease(t.Context(), release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(frozen.StepsJson), &snapshots); err != nil {
		t.Fatal(err)
	}
	if snapshots[0].Interpreter != "python3" {
		t.Fatalf(
			"release interpreter = %q, want python3",
			snapshots[0].Interpreter,
		)
	}
}
