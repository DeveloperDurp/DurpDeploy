package runner

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"

	"modernc.org/sqlite"
)

func TestNamespaceRecordingBlocksLocalOperations(t *testing.T) {
	for _, path := range []string{"deployment", "artifact", "verification"} {
		for _, outcome := range []string{"database-error", "zero-rows"} {
			t.Run(path+"/"+outcome, func(t *testing.T) {
				testNamespaceRecordingFailure(t, path, outcome)
			})
		}
	}
}

func testNamespaceRecordingFailure(t *testing.T, path, outcome string) {
	t.Helper()
	r, repo, trace := podmanFixture(t,
		`printf '%s\n' "$*" >> "$PODMAN_TRACE"`)
	if err := os.Remove(trace); err != nil {
		t.Fatal(err)
	}
	dep := stagingDeployment(t, repo)
	if err := repo.Queries.UpdateDeploymentStatus(t.Context(),
		db.UpdateDeploymentStatusParams{
			ID: dep.ID, Status: "running",
		}); err != nil {
		t.Fatal(err)
	}
	var downloads atomic.Int64
	if path == "artifact" {
		upstream := httptest.NewTLSServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				downloads.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
		t.Cleanup(upstream.Close)
		repo.ArtifactClient = &artifact.Client{
			HTTP: upstream.Client(), TempDir: t.TempDir(),
		}
		if _, err := repo.DB.ExecContext(t.Context(), `
INSERT INTO package_repositories(project_id,name,url_template,auth_type)
SELECT project_id,'package',?,'noauth' FROM releases WHERE id=?`,
			upstream.URL+"/{version}.zip", dep.ReleaseID,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.DB.ExecContext(t.Context(), `
INSERT INTO deployment_artifacts(deployment_id,repository_id,url,version,sha256,size)
SELECT ?,id,?,'v1',?,1 FROM package_repositories WHERE name='package'`,
			dep.ID, upstream.URL+"/v1.zip", strings.Repeat("a", 64),
		); err != nil {
			t.Fatal(err)
		}
	}
	rejection := "IGNORE"
	if outcome == "database-error" {
		rejection = "FAIL, 'namespace unavailable'"
	}
	if _, err := repo.DB.ExecContext(t.Context(), fmt.Sprintf(`
CREATE TRIGGER reject_container_namespace
BEFORE UPDATE OF container_namespace ON deployments
WHEN NEW.container_namespace IS NOT NULL
BEGIN SELECT RAISE(%s); END`, rejection)); err != nil {
		t.Fatal(err)
	}

	var err error
	switch path {
	case "deployment":
		_, err = r.stageDeployment(t.Context(), dep.ID, nil)
	case "artifact":
		_, err = r.stageArtifact(t.Context(), dep.ID)
	case "verification":
		err = r.verifyBash(t.Context(), t.Context(), dep.ID, 0,
			nil, nil, artifactStage{})
	}

	assertNamespaceRecordingError(t, outcome, err)
	if len(r.staging) != 0 || downloads.Load() != 0 {
		t.Fatalf("staging=%v downloads=%d",
			r.staging, downloads.Load())
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatalf("runtime contacted before ownership: %v", err)
	}
}

func assertNamespaceRecordingError(
	t *testing.T, outcome string, err error,
) {
	t.Helper()
	if outcome == "database-error" {
		var queryErr *sqlite.Error
		if !errors.As(err, &queryErr) ||
			errors.Is(err, errContainerCleanup) {
			t.Fatalf("database failure was lost: %v", err)
		}
	} else if !errors.Is(err, errContainerCleanup) {
		t.Fatalf("zero-row failure = %v", err)
	}
}
