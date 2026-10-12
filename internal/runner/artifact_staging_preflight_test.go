package runner

import (
	"errors"
	"strings"
	"testing"

	"modernc.org/sqlite"
)

func TestPackageStagingChecksFrozenLocalWorkBeforeDownload(t *testing.T) {
	for _, scenario := range []string{"verification", "steps-query-failed"} {
		t.Run(scenario, func(t *testing.T) {
			// Given an agent-only release and a frozen local verification step.
			_, repo, _ := podmanFixture(t, "")
			dep := stagingDeployment(t, repo)
			if _, err := repo.DB.ExecContext(
				t.Context(),
				`
UPDATE deployment_steps SET execution_target='agent' WHERE deployment_id=?;
INSERT INTO deployment_steps(deployment_id,step_index,name,script_body,execution_target)
VALUES(?,2,'Post-deployment verification','','local');
INSERT INTO package_repositories(project_id,name,url_template,auth_type)
SELECT project_id,'package','https://example.test/{version}.zip','noauth'
FROM releases WHERE id=?;
INSERT INTO deployment_artifacts(deployment_id,repository_id,url,version,sha256,size)
SELECT ?,id,'https://example.test/v1.zip','v1',?,1
FROM package_repositories WHERE name='package'`,
				dep.ID,
				dep.ID,
				dep.ReleaseID,
				dep.ID,
				strings.Repeat("a", 64),
			); err != nil {
				t.Fatal(err)
			}
			t.Setenv("DURPDEPLOY_EMBEDDED_AGENT_ENABLED", "false")
			r := New(repo, NewLogBroker())
			if r.localErr == nil {
				t.Fatal("fixture did not disable local execution")
			}
			if scenario == "steps-query-failed" {
				if _, err := repo.DB.ExecContext(
					t.Context(),
					"DROP TABLE deployment_steps",
				); err != nil {
					t.Fatal(err)
				}
			}

			// When package staging checks the complete frozen deployment.
			stage, err := r.stageArtifact(t.Context(), dep.ID)

			// Then it reports the preflight failure without a package volume.
			if err == nil || stage.volume != "" || len(r.staging) != 0 {
				t.Fatalf(
					"stage=%+v resources=%v error=%v",
					stage,
					r.staging,
					err,
				)
			}
			if scenario == "verification" && !errors.Is(err, r.localErr) {
				t.Fatalf("local verification was omitted: %v", err)
			}
			if scenario == "steps-query-failed" {
				var queryError *sqlite.Error
				if !errors.As(err, &queryError) {
					t.Fatalf("frozen step failure was lost: %v", err)
				}
			}
		})
	}
}
