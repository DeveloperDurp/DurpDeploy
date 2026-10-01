//go:build e2e

package api_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func (f *artifactE2E) verifyLegacyArtifactVariable(t *testing.T) {
	t.Helper()
	project := seedProject(t, f.h.repo)
	release, err := f.h.repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "legacy",
			StepsJson: `[{"name":"default","script_body":"test \"$ARTIFACT_PATH\" = /legacy/package","container_image":"docker.io/library/bash:5.2"},{"name":"selected","script_body":"test \"$ARTIFACT_PATH\" = /legacy/package","container_image":"docker.io/library/bash:5.2","variable_names":["ARTIFACT_PATH"]}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	value, err := f.h.repo.EncryptValue(
		sql.NullString{String: "/legacy/package", Valid: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateReleaseVariable(
		t.Context(),
		db.CreateReleaseVariableParams{
			ReleaseID: release.ID,
			Name:      "ARTIFACT_PATH",
			Value:     value,
		},
	); err != nil {
		t.Fatal(err)
	}
	data := f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/projects/%d/deployments", project.ID),
		map[string]int64{
			"release_id":     release.ID,
			"environment_id": f.environment.ID,
		},
		201,
	)
	var deployment db.Deployment
	if err := json.Unmarshal(data, &deployment); err != nil {
		t.Fatal(err)
	}
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
}
