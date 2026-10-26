//go:build e2e

package api_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func artifactSafetyLimits(field string, accepted bool) config.ArtifactLimits {
	l := config.Defaults().Limits.Artifact
	switch field {
	case "bytes":
		l.ExtractedBytes = 63
		if accepted {
			l.ExtractedBytes++
		}
	case "files":
		l.Files = 1
		if accepted {
			l.Files++
		}
	case "nodes":
		l.Files = 2
		l.StagingNodes = 3
		if accepted {
			l.StagingNodes++
		}
	case "metadata":
		l.MetadataBytes = 1
		if accepted {
			l.MetadataBytes = 8192
		}
	}
	return l
}

func configureSafetyArchive(t *testing.T, f *artifactE2E) {
	t.Helper()
	var data bytes.Buffer
	w := zip.NewWriter(&data)
	for _, name := range []string{"a/b/app.txt", "second.txt"} {
		entry, err := w.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(strings.Repeat("x", 32)))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	filename := filepath.Join(t.TempDir(), "safety.zip")
	require.NoError(t, os.WriteFile(filename, data.Bytes(), 0600))
	f.changePackage("file:" + filename)
}

func newArtifactSafetyFixture(
	t *testing.T,
	field string,
	accepted bool,
) *artifactE2E {
	t.Helper()
	s := config.Defaults()
	s.Container.Enabled = "false"
	s.Limits.Artifact = artifactSafetyLimits(field, accepted)
	f := newArtifactPolicyFixture(t, s)
	configureSafetyArchive(t, f)
	t.Setenv("DURPDEPLOY_LIMITS_ARTIFACT_FILES", "0")
	return f
}

func TestConfiguredArtifactSafetyAPIWebE2E(t *testing.T) {
	for _, field := range []string{"bytes", "files", "nodes", "metadata"} {
		for _, accepted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", field, accepted), func(t *testing.T) {
				f := newArtifactSafetyFixture(t, field, accepted)
				want := 422
				if accepted {
					want = 200
				}
				f.api(
					t,
					"POST",
					f.base()+"/package-repository/test",
					map[string]string{"version": "1"},
					want,
				)
				page := f.web(
					t,
					"POST",
					fmt.Sprintf(
						"/projects/%d/package-repository/test",
						f.project.ID,
					),
					url.Values{"version": {"1"}},
					200,
				)
				if accepted {
					require.Contains(t, page, "ZIP validation passed")
				} else {
					require.Contains(t, page, "Could not retrieve and validate this ZIP")
				}
				entries, err := os.ReadDir(f.h.repo.ArtifactClient.TempDir)
				require.NoError(t, err)
				require.Empty(t, entries)
			})
		}
	}
}

func TestConfiguredArtifactSafetyRemoteAPIE2E(t *testing.T) {
	f := newArtifactSafetyFixture(t, "files", true)
	f.environment = seedEnv(t, f.h.repo)
	seedIncompatiblePolicyAgent(t, f)
	f.api(
		t,
		"POST",
		f.base()+"/steps",
		map[string]any{
			"name":                 "Remote safety policy",
			"script_body":          "echo must-not-run",
			"execution_target":     "agent",
			"agent_execution_mode": "host",
		},
		201,
	)
	release := verificationRelease(t, f, "1")
	deployment := verificationDeploy(t, f, release)
	path := fmt.Sprintf("/api/v1/deployments/%d", deployment.ID)
	require.Eventually(t, func() bool {
		var d db.Deployment
		require.NoError(t, json.Unmarshal(f.api(t, "GET", path, nil, 200), &d))
		return d.Status == "failed"
	}, 10*time.Second, 20*time.Millisecond)
	require.Contains(
		t,
		string(f.api(t, "GET", path+"/logs", nil, 200)),
		"resource policy is incompatible",
	)
}

func TestConfiguredArtifactSafetyContainerAPIE2E(t *testing.T) {
	s := config.Defaults()
	s.Container.Runtime = os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
	s.Container.Namespace = "artifact-safety-" + uuid.NewString()
	s.Limits.Artifact = artifactSafetyLimits("nodes", true)
	s.Limits.Artifact.ExtractedBytes = 64
	s.Limits.Artifact.MetadataBytes = 8192
	f := newArtifactPolicyFixture(t, s)
	require.True(t, f.h.runner.ContainerRuntimeReady())
	f.environment = seedEnv(t, f.h.repo)
	configureSafetyArchive(t, f)
	f.api(
		t,
		"POST",
		f.base()+"/steps",
		map[string]any{
			"name":            "Configured package",
			"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
			"script_body": `set -eu
test "$(wc -c < "$ARTIFACT_PATH/a/b/app.txt")" -eq 32
test "$(wc -c < "$ARTIFACT_PATH/second.txt")" -eq 32
! touch "$ARTIFACT_PATH/forbidden"
echo safety-package-ready
while [ ! -e /tmp/finish ]; do sleep 0.1; done
echo safety-package-verified`,
		},
		201,
	)
	release := verificationRelease(t, f, "1")
	deployment := verificationDeploy(t, f, release)
	path := fmt.Sprintf("/api/v1/deployments/%d", deployment.ID)
	waitVerificationLog(t, f, path, "safety-package-ready")
	inspectArtifactSafetyRuntime(t, s)
	require.Eventually(t, func() bool {
		var d db.Deployment
		require.NoError(t, json.Unmarshal(f.api(t, "GET", path, nil, 200), &d))
		return d.Status == "succeeded"
	}, 45*time.Second, 50*time.Millisecond)
	require.Contains(
		t,
		string(f.api(t, "GET", path+"/logs", nil, 200)),
		"safety-package-verified",
	)
	entries, err := os.ReadDir(f.h.repo.ArtifactClient.TempDir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func inspectArtifactSafetyRuntime(t *testing.T, s config.Config) {
	t.Helper()
	ids := strings.Fields(
		stagingRuntime(
			t,
			"ps",
			"--quiet",
			"--filter=label=io.durpdeploy.namespace="+s.Container.Runtime+":"+s.Container.Namespace,
		),
	)
	found := false
	workload := ""
	for _, id := range ids {
		name := stagingRuntime(t, "inspect", "--format={{.Name}}", id)
		if !strings.Contains(name, "-keeper") {
			workload = id
			continue
		}
		volume := strings.TrimSpace(
			stagingRuntime(
				t,
				"inspect",
				`--format={{range .Mounts}}{{if eq .Destination "/artifacts"}}{{.Name}}{{end}}{{end}}`,
				id,
			),
		)
		if volume == "" {
			continue
		}
		found = true
		capacity := s.Limits.Artifact.ExtractedBytes + s.Limits.Artifact.StagingNodes*int64(
			os.Getpagesize(),
		)
		memory := strings.TrimSpace(
			stagingRuntime(t, "inspect", "--format={{.HostConfig.Memory}}", id),
		)
		require.Equal(
			t,
			strconv.FormatInt(
				capacity+s.Limits.Container.HelperMemoryOverheadBytes,
				10,
			),
			memory,
		)
		if s.Container.Runtime == "docker" {
			options := stagingRuntime(
				t,
				"volume",
				"inspect",
				"--format={{json .Options}}",
				volume,
			)
			require.Contains(
				t,
				options,
				fmt.Sprintf(
					"size=%d,nr_inodes=%d",
					capacity,
					2*(s.Limits.Artifact.StagingNodes+1),
				),
			)
		}
	}
	require.True(t, found, "artifact helper was not inspected")
	require.NotEmpty(t, workload)
	stagingRuntime(t, "exec", workload, "touch", "/tmp/finish")
}

func TestConfiguredArtifactApprovalNodesAPIE2E(t *testing.T) {
	for _, nodes := range []int64{2, 3} {
		t.Run(fmt.Sprint(nodes), func(t *testing.T) {
			s := config.Defaults()
			s.Container.Runtime = os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
			s.Container.Namespace = "artifact-gate-policy-" + uuid.NewString()
			s.Limits.Artifact.Files = 2
			s.Limits.Artifact.StagingNodes = nodes
			f := newArtifactPolicyFixture(t, s)
			f.environment = seedEnv(t, f.h.repo)
			f.api(
				t,
				"POST",
				f.base()+"/steps",
				map[string]any{
					"name":                   "Generate bounded context",
					"container_image":        "harbor.durp.info/dockerhub/library/bash:5.2",
					"approval_artifact_path": "plan",
					"approval_review_path":   "review",
					"approval_review_format": "summary",
					"script_body": `set -eu
printf plan > "$DURPDEPLOY_STAGE_DIR/plan"
printf '{"create":0}' > "$DURPDEPLOY_STAGE_DIR/review"
printf extra > "$DURPDEPLOY_STAGE_DIR/extra"`,
				},
				201,
			)
			f.api(
				t,
				"POST",
				f.base()+"/steps",
				map[string]any{
					"name":            "After approval",
					"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
					"script_body":     "echo must-not-run-before-approval",
				},
				201,
			)
			release := verificationRelease(t, f, "1")
			d := verificationDeploy(t, f, release)
			path := fmt.Sprintf("/api/v1/deployments/%d", d.ID)
			want := "failed"
			if nodes == 3 {
				want = "awaiting_artifact_approval"
			}
			require.Eventually(t, func() bool {
				var row db.Deployment
				require.NoError(
					t,
					json.Unmarshal(f.api(t, "GET", path, nil, 200), &row),
				)
				return row.Status == want
			}, 45*time.Second, 50*time.Millisecond)
			if nodes == 3 {
				f.api(t, "POST", path+"/cancel", nil, 200)
				require.Eventually(t, func() bool {
					var row db.Deployment
					require.NoError(
						t,
						json.Unmarshal(f.api(t, "GET", path, nil, 200), &row),
					)
					return row.Status == "cancelled"
				}, 15*time.Second, 50*time.Millisecond)
			}
		})
	}
}
