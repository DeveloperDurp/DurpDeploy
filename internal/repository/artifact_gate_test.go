package repository

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"
)

type retryGateSource struct {
	*bytes.Reader
	reads int
	seeks int
}

func (s *retryGateSource) Read(data []byte) (int, error) {
	s.reads++
	if s.reads == 2 {
		n, _ := s.Reader.Read(data[:1])
		return n, sqliteCodeError(sqliteBusyCode)
	}
	return s.Reader.Read(data)
}

func (s *retryGateSource) Seek(offset int64, whence int) (int64, error) {
	s.seeks++
	return s.Reader.Seek(offset, whence)
}

func TestArtifactGateClaimsAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		// Given: a durable gate shared by two database connections.
		first, second := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		box, err := secret.NewBox(make([]byte, 32))
		if err != nil {
			t.Fatal(err)
		}
		first.SetSecretBox(box)
		second.SetSecretBox(box)
		if _, err := first.Queries.UpdateRelease(
			t.Context(),
			db.UpdateReleaseParams{
				ID:        1,
				ProjectID: 1,
				Version:   "gate",
				StepsJson: `[{"name":"generate","container_image":"bash:5.2","approval_artifact_path":"plan","approval_review_path":"review","approval_review_format":"summary"},{"name":"apply","container_image":"bash:5.2"}]`,
			},
		); err != nil {
			t.Fatal(err)
		}
		result, err := first.CreateDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID:     1,
				EnvironmentID: 1,
				Status:        "pending",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		id := result.Deployment.ID
		if _, _, err := first.BeginArtifactGateRun(
			t.Context(),
			id,
		); err != nil {
			t.Fatal(err)
		}
		data := bytes.Repeat([]byte("immutable-sensitive-plan"), 100000)
		source := &retryGateSource{Reader: bytes.NewReader(data)}
		hash := sha256.Sum256(data)
		checksum := hex.EncodeToString(hash[:])
		now, err := first.Queries.CurrentUnixTime(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := first.SaveArtifactGate(
			t.Context(),
			db.CreateArtifactGateParams{
				DeploymentID:   id,
				StepIndex:      0,
				ArtifactPath:   "plan",
				ArtifactSha256: checksum,
				BundleSha256:   checksum,
				BundleSize:     int64(len(data)),
				Review:         `{}`,
				CreatedAt:      now,
				ExpiresAt:      now + 86400,
			},
			source,
		); err != nil {
			t.Fatal(err)
		}
		if source.seeks < 2 {
			t.Fatal("publication did not rewind after a partial chunk retry")
		}
		stored, err := first.Queries.GetArtifactGate(
			t.Context(),
			db.GetArtifactGateParams{
				DeploymentID: id, StepIndex: 0,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := first.WriteArtifactGateBundle(
			t.Context(),
			first.Queries,
			stored,
			io.Discard,
		); err != nil {
			t.Fatal(err)
		}
		// Publishing is active on every engine; parent deletion must be blocked.
		if err := first.DeleteEnvironment(
			t.Context(),
			1,
		); !errors.Is(
			err,
			ErrEnvironmentHasActiveDeployment,
		) {
			t.Fatalf("delete publishing environment: %v", err)
		}
		if err := first.DeleteProject(
			t.Context(),
			1,
		); !errors.Is(
			err,
			ErrProjectHasActiveRunbook,
		) {
			t.Fatalf("delete publishing project: %v", err)
		}
		if err := first.DeleteRelease(
			t.Context(),
			1,
			1,
		); !errors.Is(
			err,
			ErrReleaseHasActiveDeployment,
		) {
			t.Fatalf("delete publishing release: %v", err)
		}
		if err := first.PauseArtifactGate(t.Context(), id, 1); err != nil {
			t.Fatal(err)
		}
		// When: two connections approve and subsequently claim the continuation.
		var group sync.WaitGroup
		results := make(chan error, 2)
		for _, repo := range []*Repository{first, second} {
			group.Go(
				func() { results <- repo.ApproveArtifact(t.Context(), id, 0, 1, 1, checksum) },
			)
		}
		group.Wait()
		close(results)
		accepted := 0
		for err := range results {
			if err == nil {
				accepted++
			} else if !errors.Is(err, ErrArtifactGate) {
				t.Fatal(err)
			}
		}
		if accepted != 1 {
			t.Fatalf("approvals=%d", accepted)
		}
		_, gated, err := first.BeginArtifactGateRun(t.Context(), id)
		if err != nil || !gated {
			t.Fatalf("claim gated=%v err=%v", gated, err)
		}
		_, _, err = second.BeginArtifactGateRun(t.Context(), id)
		// Then: exactly one approval and execution claim are accepted.
		if !errors.Is(err, ErrArtifactGate) {
			t.Fatalf("duplicate claim=%v", err)
		}
	})
}
