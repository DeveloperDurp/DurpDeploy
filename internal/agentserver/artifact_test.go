package agentserver_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"
)

func seedAgentArtifact(
	t *testing.T,
	f agentFixture,
	deploymentID int64,
	onFetch func(),
) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("package.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, "verified package"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data := buffer.Bytes()
	upstream := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer private-credential" {
				w.WriteHeader(401)
				return
			}
			if onFetch != nil {
				onFetch()
			}
			if _, err := w.Write(data); err != nil {
				t.Error(err)
			}
		}),
	)
	t.Cleanup(upstream.Close)
	f.repo.ArtifactClient = &artifact.Client{HTTP: upstream.Client()}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	f.repo.SetSecretBox(box)
	deployment, err := f.repo.Queries.GetDeployment(t.Context(), deploymentID)
	if err != nil {
		t.Fatal(err)
	}
	release, err := f.repo.Queries.GetRelease(t.Context(), deployment.ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := f.repo.CreatePackageRepository(
		t.Context(),
		db.CreatePackageRepositoryParams{
			ProjectID:   release.ProjectID,
			Name:        "packages",
			UrlTemplate: upstream.URL + "/{version}.zip",
			AuthType:    "bearer",
			Credential:  "private-credential",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if err := f.repo.Queries.CreateReleaseArtifact(
		t.Context(),
		db.CreateReleaseArtifactParams{
			ReleaseID:    release.ID,
			RepositoryID: repository.ID,
			Url:          upstream.URL + "/1.zip",
			Version:      "1",
			Sha256:       hex.EncodeToString(digest[:]),
			Size:         int64(len(data)),
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Queries.CopyReleaseArtifactToDeployment(
		t.Context(),
		db.CopyReleaseArtifactToDeploymentParams{
			DeploymentID: deploymentID,
			ReleaseID:    release.ID,
		},
	); err != nil {
		t.Fatal(err)
	}
	return data
}

func artifactClaimBody(t *testing.T, claim string) string {
	t.Helper()
	var request struct {
		Token string `json:"claim_token"`
	}
	if err := json.Unmarshal([]byte(claim), &request); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"claim_token": request.Token})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestAgentArtifactServesVerifiedZIPForLiveClaim(t *testing.T) {
	// Given
	f := newAgentFixture(t)
	id, claim := claimedRemoteStep(t, f)
	data := seedAgentArtifact(t, f, id, nil)
	// When
	response := postAgent(
		t,
		f,
		fmt.Sprintf("/agent/v1/deployments/%d/artifact", id),
		artifactClaimBody(t, claim),
	)
	// Then
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !bytes.Equal(contents, data) {
		t.Fatalf("status=%d body=%s", response.StatusCode, contents)
	}
	if response.Header.Get("X-Artifact-SHA256") == "" ||
		response.Header.Get("Content-Type") != "application/zip" {
		t.Fatal("artifact verification metadata missing")
	}
	if strings.Contains(fmt.Sprint(response.Header), "private-credential") {
		t.Fatal("credential exposed")
	}
}

func TestAgentArtifactRejectsInvalidOrExpiredClaim(t *testing.T) {
	for _, scenario := range []string{"wrong token", "expired", "cancelled", "different deployment"} {
		t.Run(scenario, func(t *testing.T) {
			// Given
			f := newAgentFixture(t)
			id, claim := claimedRemoteStep(t, f)
			body := artifactClaimBody(t, claim)
			path := fmt.Sprintf("/agent/v1/deployments/%d/artifact", id)
			switch scenario {
			case "wrong token":
				body = `{"claim_token":"incorrect"}`
			case "expired":
				if _, err := f.repo.DB.Exec(
					"UPDATE remote_step_runs SET claim_expires_at=0",
				); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				if _, err := f.repo.DB.Exec(
					"UPDATE remote_step_runs SET state='cancelled'",
				); err != nil {
					t.Fatal(err)
				}
			case "different deployment":
				path = fmt.Sprintf("/agent/v1/deployments/%d/artifact", id+1)
			}
			// When
			response := postAgent(t, f, path, body)
			// Then
			if response.StatusCode != 409 {
				t.Fatalf("status=%d", response.StatusCode)
			}
		})
	}
}

func TestAgentArtifactRechecksClaimAfterDownload(t *testing.T) {
	// Given
	f := newAgentFixture(t)
	id, claim := claimedRemoteStep(t, f)
	seedAgentArtifact(t, f, id, func() {
		if _, err := f.repo.DB.Exec(
			"UPDATE remote_step_runs SET state='cancelled'",
		); err != nil {
			t.Error(err)
		}
	})
	// When
	response := postAgent(
		t,
		f,
		fmt.Sprintf("/agent/v1/deployments/%d/artifact", id),
		artifactClaimBody(t, claim),
	)
	// Then
	if response.StatusCode != 409 {
		t.Fatalf("status=%d", response.StatusCode)
	}
}

func TestAgentArtifactReturnsNotFoundWithoutPin(t *testing.T) {
	// Given
	f := newAgentFixture(t)
	id, claim := claimedRemoteStep(t, f)
	// When
	response := postAgent(
		t,
		f,
		fmt.Sprintf("/agent/v1/deployments/%d/artifact", id),
		artifactClaimBody(t, claim),
	)
	// Then
	if response.StatusCode != 404 {
		t.Fatalf("status=%d", response.StatusCode)
	}
}
