package agentserver_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"durpdeploy/internal/artifact"
)

func TestAgentArtifactRecoversAfterUpstreamFailure(t *testing.T) {
	// Given
	f := newAgentFixture(t)
	id, claim := claimedRemoteStep(t, f)
	data := seedAgentArtifact(t, f, id, nil)
	var available atomic.Bool
	upstream := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer private-credential" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if !available.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if _, err := w.Write(data); err != nil {
				t.Error(err)
			}
		},
	))
	t.Cleanup(upstream.Close)
	workspace := t.TempDir()
	f.repo.ArtifactClient = &artifact.Client{
		HTTP: upstream.Client(), TempDir: workspace,
	}
	if _, err := f.repo.DB.ExecContext(t.Context(),
		"UPDATE package_repositories SET url_template=?",
		upstream.URL+"/{version}.zip",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.DB.ExecContext(t.Context(),
		"UPDATE deployment_artifacts SET url=? WHERE deployment_id=?",
		upstream.URL+"/1.zip", id,
	); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/agent/v1/deployments/%d/artifact", id)
	body := artifactClaimBody(t, claim)

	// When
	for _, status := range []int{
		http.StatusBadGateway, http.StatusOK,
	} {
		response := postAgent(t, f, path, body)
		contents, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}

		// Then
		if response.StatusCode != status {
			t.Fatalf("artifact status=%d want=%d", response.StatusCode, status)
		}
		if status == http.StatusOK && !bytes.Equal(contents, data) {
			t.Fatal("recovered download differs from the pinned package")
		}
		if status == http.StatusBadGateway && len(contents) != 0 {
			t.Fatalf("upstream failure exposed response bytes: %q", contents)
		}
		files, err := os.ReadDir(workspace)
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 0 {
			t.Fatalf("artifact request retained temporary files: %v", files)
		}
		available.Store(true)
	}
}
