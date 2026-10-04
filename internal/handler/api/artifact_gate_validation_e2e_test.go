//go:build e2e

package api_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestArtifactGatePublishingRecoverySafetyE2E(t *testing.T) {
	f, deployment, _ := newGateDeployment(t)
	if _, err := f.h.repo.DB.Exec(
		"UPDATE deployments SET status='publishing_artifact' WHERE id=?",
		deployment.ID,
	); err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.WithQueueMaintenanceTx(
		t.Context(),
		func(ctx context.Context, q *db.Queries) error {
			now, err := q.CurrentUnixTime(ctx)
			if err != nil {
				return err
			}
			stamp := sql.NullInt64{Int64: now, Valid: true}
			if _, err := q.MarkUnreconciledLocalDeployments(
				ctx,
				stamp,
			); err != nil {
				return err
			}
			if _, err := q.FailOrphanedDeployments(ctx, stamp); err != nil {
				return err
			}
			return q.CancelTerminalArtifactGates(ctx)
		},
	); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/deployments/%d", deployment.ID)
	if body := f.api(
		t,
		"GET",
		path+"/status",
		nil,
		200,
	); !strings.Contains(
		string(body),
		`"status":"cleanup_unconfirmed"`,
	) {
		t.Fatalf("publication cleanup certainty lost: %s", body)
	}
	f.api(t, "POST", path+"/redeploy", nil, 409)
	f.api(t, "DELETE", f.base(), nil, 409)
	f.web(
		t,
		"POST",
		fmt.Sprintf("/deployments/%d/redeploy", deployment.ID),
		nil,
		409,
	)
}

type gatePreparationReader struct {
	*bytes.Reader
	beforeRead func()
}

func (r *gatePreparationReader) Read(data []byte) (int, error) {
	if r.beforeRead != nil {
		r.beforeRead()
		r.beforeRead = nil
	}
	return r.Reader.Read(data)
}

func TestArtifactGatePublicationAllowsWritesDuringPreparationE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name":                   "Generate",
		"script_body":            "true",
		"container_image":        "docker.io/library/bash:5.2",
		"approval_artifact_path": "plan",
		"approval_review_path":   "review",
		"approval_review_format": "summary",
	}, 201)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name":            "Apply",
		"script_body":     "true",
		"container_image": "docker.io/library/bash:5.2",
	}, 201)
	release := verificationRelease(t, f, "publication-lock")
	created, err := f.h.repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: f.environment.ID,
			Status:        "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	id := created.Deployment.ID
	if _, _, err := f.h.repo.BeginArtifactGateRun(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	var bundle bytes.Buffer
	archive := tar.NewWriter(&bundle)
	for name, value := range map[string]string{"plan": gateBytes, "review": `{"create":1}`} {
		if err := archive.WriteHeader(
			&tar.Header{Name: name, Mode: 0644, Size: int64(len(value))},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	artifactHash, bundleHash := sha256.Sum256(
		[]byte(gateBytes),
	), sha256.Sum256(
		bundle.Bytes(),
	)
	source := &gatePreparationReader{
		Reader: bytes.NewReader(bundle.Bytes()),
		beforeRead: func() {
			// This HTTP write fails if publication already holds SQLite's writer.
			f.api(
				t,
				"PUT",
				f.base(),
				map[string]string{"name": "Write during preparation"},
				200,
			)
		},
	}
	now, err := f.h.repo.Queries.CurrentUnixTime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.SaveArtifactGate(
		t.Context(),
		db.CreateArtifactGateParams{
			DeploymentID: id,
			StepIndex:    0,
			ArtifactPath: "plan",
			ArtifactSha256: hex.EncodeToString(
				artifactHash[:],
			),
			BundleSha256: hex.EncodeToString(bundleHash[:]),
			BundleSize: int64(
				bundle.Len(),
			),
			Review:    `{"create":1}`,
			CreatedAt: now,
			ExpiresAt: now + 86400,
		},
		source,
	); err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.PauseArtifactGate(t.Context(), id, 1); err != nil {
		t.Fatal(err)
	}
	if got := f.api(
		t,
		"GET",
		gateAPIPath(id)+"/0/artifact",
		nil,
		200,
	); string(
		got,
	) != gateBytes {
		t.Fatalf("published artifact changed: %q", got)
	}
	f.api(t, "POST", fmt.Sprintf("/api/v1/deployments/%d/cancel", id), nil, 200)
}

func TestArtifactGateMalformedSummaryE2E(t *testing.T) {
	for _, review := range []string{"null", "{}", `{"creates":5}`, `{"create":null}`} {
		t.Run(review, func(t *testing.T) {
			f := newArtifactE2E(t)
			f.api(t, "POST", f.base()+"/steps", map[string]string{
				"name":                   "Generate",
				"container_image":        "docker.io/library/bash:5.2",
				"approval_artifact_path": "plan",
				"approval_review_path":   "review",
				"approval_review_format": "summary",
				"script_body": fmt.Sprintf(
					"printf plan > \"$DURPDEPLOY_STAGE_DIR/plan\"; printf '%%s' '%s' > \"$DURPDEPLOY_STAGE_DIR/review\"",
					review,
				),
			}, 201)
			f.api(t, "POST", f.base()+"/steps", map[string]string{
				"name": "Apply", "script_body": "exit 9",
				"container_image": "docker.io/library/bash:5.2",
			}, 201)
			d := verificationDeploy(
				t,
				f,
				verificationRelease(t, f, "invalid-review"),
			)
			f.completion(t, d.ID, events.DeploymentFailed)
			if body := f.api(
				t,
				"GET",
				gateAPIPath(d.ID),
				nil,
				200,
			); strings.TrimSpace(
				string(body),
			) != "[]" {
				t.Fatalf("malformed review published: %s", body)
			}
		})
	}
}

func TestArtifactGateLegacyReservedVariableE2E(t *testing.T) {
	f := newArtifactE2E(t)
	release := verificationRelease(t, f, "legacy-approved-variable")
	if _, err := f.h.repo.Queries.CreateVariable(
		t.Context(),
		db.CreateVariableParams{
			ProjectID: f.project.ID, Name: "DURPDEPLOY_APPROVED_DIR",
		},
	); err != nil {
		t.Fatal(err)
	}
	f.api(
		t,
		"POST",
		f.base()+"/releases",
		map[string]string{"version": "blocked"},
		422,
	)
	f.web(
		t,
		"POST",
		fmt.Sprintf("/projects/%d/releases", f.project.ID),
		url.Values{"version": {"blocked-web"}},
		422,
	)
	if _, err := f.h.repo.Queries.CreateReleaseVariable(
		t.Context(),
		db.CreateReleaseVariableParams{
			ReleaseID: release.ID, Name: "DURPDEPLOY_APPROVED_DIR",
		},
	); err != nil {
		t.Fatal(err)
	}
	f.api(t, "POST", f.base()+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": f.environment.ID,
	}, 422)
	f.web(
		t,
		"POST",
		fmt.Sprintf("/projects/%d/deploy", f.project.ID),
		url.Values{
			"release_id": {
				fmt.Sprint(release.ID),
			},
			"environment_id": {fmt.Sprint(f.environment.ID)},
		},
		422,
	)
}

func TestArtifactGateInvalidFormPreservesContainerFieldsE2E(t *testing.T) {
	f := newArtifactE2E(t)
	for _, surface := range []string{"step", "template"} {
		apiPath, webPath := f.base()+"/steps", fmt.Sprintf(
			"/projects/%d/steps",
			f.project.ID,
		)
		if surface == "template" {
			apiPath, webPath = "/api/v1/templates", "/templates"
		}
		f.api(t, "POST", apiPath, map[string]any{
			"name":                   "Invalid gate",
			"script_body":            "true",
			"container_image":        "docker.io/library/bash:5.2",
			"variable_names":         []string{"KEEP_THIS"},
			"approval_artifact_path": "../plan",
			"approval_review_path":   "review",
			"approval_review_format": "summary",
		}, 400)
		page := f.web(t, "POST", webPath, url.Values{
			"name":        {"Invalid gate"},
			"script_body": {"true"},
			"container_image": {
				"docker.io/library/bash:5.2",
			},
			"variable_names": {"KEEP_THIS"},
			"approval_artifact_path": {
				"../plan",
			},
			"approval_review_path":   {"review"},
			"approval_review_format": {"summary"},
		}, 422)
		if !strings.Contains(page, `value="docker.io/library/bash:5.2"`) ||
			!strings.Contains(page, `value="KEEP_THIS"`) {
			t.Fatalf("%s validation cleared container fields", surface)
		}
	}
}
