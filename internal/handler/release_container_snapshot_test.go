package handler

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestCreateReleaseSnapshotFreezesContainerMetadata(t *testing.T) {
	conn, err := migrate.Run(filepath.Join(t.TempDir(), "container.db") +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	repo := repository.New(conn)
	project, err := repo.Queries.CreateProject(
		t.Context(), db.CreateProjectParams{Name: "container-release"},
	)
	if err != nil {
		t.Fatal(err)
	}
	step, err := repo.CreateStepWithPlacement(
		t.Context(),
		db.CreateStepParams{
			ProjectID: project.ID, Name: "deploy", ScriptBody: "echo hi",
			Interpreter:    "bash",
			ContainerImage: "alpine:3.20",
			VariableNames:  `["APP_ENV","DEBUG"]`,
		},
		"local", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateStepWithPlacement(
		t.Context(),
		db.CreateStepParams{
			ProjectID: project.ID, Name: "legacy", ScriptBody: "echo old",
			Interpreter: "bash", VariableNames: "",
		},
		"local", nil,
	); err != nil {
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
	if len(snapshots) != 2 {
		t.Fatalf("snapshots = %+v", snapshots)
	}
	if snapshots[0].ContainerImage != "alpine:3.20" {
		t.Fatalf(
			"snapshot image = %q, want alpine:3.20",
			snapshots[0].ContainerImage,
		)
	}
	if len(snapshots[0].VariableNames) != 2 ||
		snapshots[0].VariableNames[0] != "APP_ENV" ||
		snapshots[0].VariableNames[1] != "DEBUG" {
		t.Fatalf(
			"snapshot variable names = %+v",
			snapshots[0].VariableNames,
		)
	}
	if snapshots[1].ContainerImage != "" ||
		len(snapshots[1].VariableNames) != 0 {
		t.Fatalf("legacy snapshot = %+v", snapshots[1])
	}

	step, err = repo.Queries.GetStep(t.Context(), step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.UpdateStep(t.Context(), db.UpdateStepParams{
		ID: step.ID, Name: step.Name, ScriptBody: step.ScriptBody,
		SortOrder: step.SortOrder, TimeoutSeconds: step.TimeoutSeconds,
		MaxRetries: step.MaxRetries, Interpreter: step.Interpreter,
		ContainerImage: "busybox:1.36", VariableNames: step.VariableNames,
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
	if snapshots[0].ContainerImage != "alpine:3.20" {
		t.Fatalf(
			"release image = %q, want frozen alpine:3.20",
			snapshots[0].ContainerImage,
		)
	}
}
