package repository_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

func TestDeferredPackageConcurrentPullsLockOneChecksum(t *testing.T) {
	for _, different := range []bool{false, true} {
		name := "same bytes"
		if different {
			name = "different bytes"
		}
		t.Run(name, func(t *testing.T) {
			f := newArtifactFixture(t)
			payloads := [][]byte{
				deferredZIP(t, "first"),
				deferredZIP(t, "second"),
			}
			if !different {
				payloads[1] = payloads[0]
			}
			ready := make(chan struct{}, 2)
			releaseResponses := make(chan struct{})
			var requests atomic.Int64
			upstream := httptest.NewTLSServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					index := requests.Add(1) - 1
					ready <- struct{}{}
					select {
					case <-releaseResponses:
					case <-r.Context().Done():
						return
					}
					if _, err := w.Write(payloads[index%2]); err != nil {
						t.Error(err)
					}
				}))
			t.Cleanup(upstream.Close)
			t.Cleanup(func() {
				select {
				case <-releaseResponses:
				default:
					close(releaseResponses)
				}
			})
			f.repo.ArtifactClient = &artifact.Client{
				HTTP: upstream.Client(), TempDir: t.TempDir(),
			}
			if _, err := f.repo.SaveProjectPackageRepository(t.Context(),
				f.project.ID, artifact.Repository{
					URLTemplate: upstream.URL + "/{version}.zip",
					AuthType:    "noauth",
				}); err != nil {
				t.Fatal(err)
			}
			release, err := handler.CreateReleaseSnapshot(t.Context(),
				f.repo, f.project.ID, "deferred")
			if err != nil {
				t.Fatal(err)
			}
			args := db.CreateDeploymentParams{ReleaseID: release.ID,
				EnvironmentID: f.environment.ID, Status: "pending"}
			first, err := f.repo.CreateDeployment(t.Context(), args)
			if err != nil {
				t.Fatal(err)
			}
			second, err := f.repo.CreateDeployment(t.Context(), args)
			if err != nil {
				t.Fatal(err)
			}
			_, runbook, err := f.repo.SaveRunbook(
				t.Context(),
				repository.RunbookSave{
					ProjectID: f.project.ID, Name: "deferred-copy",
					ArtifactReleaseID: release.ID, StepsJSON: release.StepsJson,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			results := make(chan error, 2)
			for _, id := range []int64{first.Deployment.ID, second.Deployment.ID} {
				go func() {
					download, err := f.repo.DownloadDeploymentArtifact(
						t.Context(),
						id,
					)
					if download.Path != "" {
						err = errors.Join(err, os.Remove(download.Path))
					}
					results <- err
				}()
			}
			for range 2 {
				select {
				case <-ready:
				case <-time.After(5 * time.Second):
					t.Fatal("both first pulls did not reach the package server")
				}
			}
			close(releaseResponses)
			failures := 0
			for range 2 {
				err := <-results
				if err != nil {
					if !errors.Is(err, artifact.ErrChecksum) {
						t.Fatal(err)
					}
					failures++
				}
			}
			if (failures == 1) != different || failures > 1 {
				t.Fatalf(
					"different=%t checksum failures=%d",
					different,
					failures,
				)
			}
			pin, err := f.repo.Queries.GetReleaseArtifact(
				t.Context(),
				release.ID,
			)
			if err != nil || pin.Sha256 == "" || pin.Size <= 0 {
				t.Fatalf("first pull did not pin release: %+v (%v)", pin, err)
			}
			for _, id := range []int64{first.Deployment.ID, second.Deployment.ID} {
				copy, err := f.repo.Queries.GetDeploymentArtifact(
					t.Context(),
					id,
				)
				if err != nil || copy.Sha256 != pin.Sha256 ||
					copy.Size != pin.Size {
					t.Fatalf(
						"pending deployment copy diverged: %+v (%v)",
						copy,
						err,
					)
				}
			}
			copy, err := f.repo.Queries.GetReleaseArtifact(
				t.Context(),
				runbook.ReleaseID,
			)
			if err != nil || copy.Sha256 != pin.Sha256 ||
				copy.Size != pin.Size {
				t.Fatalf("pending runbook copy diverged: %+v (%v)", copy, err)
			}
		})
	}
}

func deferredZIP(t *testing.T, content string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("app.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
