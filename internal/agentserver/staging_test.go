package agentserver_test

import (
	"archive/tar"
	"bytes"
	"context"
	"durpdeploy/internal/testutil"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/staging"
)

func postAgentStaging(
	t *testing.T,
	f agentFixture,
	path, body string,
) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(
		t.Context(),
		"POST",
		f.server.URL+path,
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-DurpDeploy-Staging", staging.Capability)
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func stageArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for name, data := range files {
		if err := writer.WriteHeader(
			&tar.Header{
				Name:     name,
				Mode:     0600,
				Size:     int64(len(data)),
				Typeflag: tar.TypeReg,
			},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func seedStageInput(t *testing.T, f agentFixture, id int64) []byte {
	t.Helper()
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	f.repo.SetSecretBox(box)
	snapshot, err := box.Encrypt("[]")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Queries.CreateDeploymentVariableSnapshot(
		t.Context(),
		db.CreateDeploymentVariableSnapshotParams{
			DeploymentID: id,
			Value:        snapshot,
		},
	); err != nil {
		t.Fatal(err)
	}
	data := stageArchive(t, map[string]string{"seed": "staging-secret"})
	file, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, file)
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.SaveStaging(
		t.Context(),
		repository.StagingWrite{
			Key:    db.GetStagingSnapshotParams{DeploymentID: id},
			Source: file,
		},
	); err != nil {
		t.Fatal(err)
	}
	return data
}

func stageRequest(
	t *testing.T,
	f agentFixture,
	id int64,
	token, action string,
	body []byte,
	want int,
) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(
		t.Context(),
		"POST",
		fmt.Sprintf(
			"%s/agent/v1/deployments/%d/staging/%s",
			f.server.URL,
			id,
			action,
		),
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-DurpDeploy-Claim", token)
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf(
			"%s status=%d want=%d body=%s",
			action,
			response.StatusCode,
			want,
			data,
		)
	}
	return data
}

func TestAgentStagingRequiresPublicationBeforeSuccessAndPurges(t *testing.T) {
	f := newAgentFixture(t)
	id, claim := claimedRemoteStep(t, f)
	seedStageInput(t, f, id)
	var identity struct {
		Token string `json:"claim_token"`
	}
	if err := json.Unmarshal([]byte(claim), &identity); err != nil {
		t.Fatal(err)
	}
	stageRequest(t, f, id, "wrong", "download", nil, 409)
	stageRequest(t, f, id, identity.Token, "upload", stageArchive(t, nil), 409)
	input := stageRequest(t, f, id, identity.Token, "download", nil, 200)
	if !bytes.Contains(input, []byte("staging-secret")) {
		t.Fatal("input bytes missing")
	}
	response := postAgent(
		t,
		f,
		fmt.Sprintf("/agent/v1/deployments/%d/start", id),
		claim,
	)
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal(response.StatusCode)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(claim), &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["state"] = "succeeded"
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	response = postAgent(
		t,
		f,
		fmt.Sprintf("/agent/v1/deployments/%d/result", id),
		string(body),
	)
	response.Body.Close()
	if response.StatusCode != 409 {
		t.Fatalf("success without staging=%d", response.StatusCode)
	}
	output := stageArchive(t, map[string]string{"result": "published"})
	stageRequest(t, f, id, identity.Token, "upload", output, 204)
	var auditCount int
	if err := f.repo.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='publish_staging' AND entity_id=?", id).
		Scan(&auditCount); err != nil ||
		auditCount != 1 {
		t.Fatalf("staging audit count=%d err=%v", auditCount, err)
	}
	stageRequest(t, f, id, identity.Token, "upload", output, 204)
	stageRequest(
		t,
		f,
		id,
		identity.Token,
		"upload",
		stageArchive(t, map[string]string{"result": "changed"}),
		409,
	)
	var plaintext string
	if err := f.repo.DB.QueryRow("SELECT ciphertext FROM deployment_staging_chunks WHERE producer=''").
		Scan(&plaintext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(plaintext), []byte("staging-secret")) {
		t.Fatal("staging stored in plaintext")
	}
	response = postAgent(
		t,
		f,
		fmt.Sprintf("/agent/v1/deployments/%d/result", id),
		string(body),
	)
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal(response.StatusCode)
	}
	if err := f.repo.WithDeploymentTx(
		t.Context(),
		id,
		func(ctx context.Context, q *db.Queries) error {
			return q.UpdateDeploymentStatus(
				ctx,
				db.UpdateDeploymentStatusParams{ID: id, Status: "succeeded"},
			)
		},
	); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"deployment_staging_snapshots", "deployment_staging_chunks"} {
		var count int
		if err := f.repo.DB.QueryRow("SELECT COUNT(*) FROM " + table).
			Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d rows", table, count)
		}
	}
	stageRequest(t, f, id, identity.Token, "download", nil, 409)
	stageRequest(t, f, id, identity.Token, "upload", output, 409)
}

func TestAgentStagingRejectsUnsafeUpload(t *testing.T) {
	f := newAgentFixture(t)
	id, claim := claimedRemoteStep(t, f)
	seedStageInput(t, f, id)
	var identity struct {
		Token string `json:"claim_token"`
	}
	if err := json.Unmarshal([]byte(claim), &identity); err != nil {
		t.Fatal(err)
	}
	response := postAgent(
		t,
		f,
		fmt.Sprintf("/agent/v1/deployments/%d/start", id),
		claim,
	)
	response.Body.Close()
	stageRequest(
		t,
		f,
		id,
		identity.Token,
		"upload",
		stageArchive(t, map[string]string{"../escape": "bad"}),
		422,
	)
	truncated := stageArchive(t, map[string]string{"file": strings.Repeat("x", 2048)})[:1024]
	stageRequest(t, f, id, identity.Token, "upload", truncated, 422)
	rows, err := f.repo.Queries.ListStagingSnapshots(t.Context(), id)
	if err != nil || len(rows) != 1 {
		t.Fatalf("snapshots=%v error=%v", rows, err)
	}
}
