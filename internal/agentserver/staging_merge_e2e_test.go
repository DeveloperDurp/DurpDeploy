//go:build e2e && agentstaging

package agentserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/staging"
)

// Two real agents publish through mTLS. Additional successful run fixtures
// exercise fixed aggregate limits without starting a fleet of 65 processes.
func injectStagingMergeBudget(
	ctx context.Context,
	repo *repository.Repository,
	deploymentID int64,
	scenario string,
) (result error) {
	count := 4
	if scenario == "outputs" {
		count = 63
	}
	if scenario == "entries" {
		count = 9
	}
	key := db.GetStagingSnapshotParams{DeploymentID: deploymentID}
	var sourceKey db.GetStagingSnapshotParams
	if scenario == "entries" {
		rows, err := repo.Queries.ListStagingSnapshots(ctx, deploymentID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Producer != "" {
				sourceKey = key
				sourceKey.Producer = row.Producer
				break
			}
		}
		if sourceKey.Producer == "" {
			return errors.New("missing published agent output")
		}
	}
	for i := range count {
		id := fmt.Sprintf("budget-%02d", i)
		if _, err := repo.DB.ExecContext(ctx, "INSERT INTO agents (id,name,endpoint) VALUES (?,?,'https://fixture')", id, id); err != nil {
			return err
		}
		if _, err := repo.DB.ExecContext(ctx, "INSERT INTO remote_step_runs (deployment_id,step_index,agent_id,state) VALUES (?,0,?,'succeeded')", deploymentID, id); err != nil {
			return err
		}
		key.Producer = id
		switch scenario {
		case "bytes":
			if err := repo.Queries.CreateStagingSnapshot(ctx, db.CreateStagingSnapshotParams{DeploymentID: deploymentID, Producer: id, Sha256: strings.Repeat("0", 64), Size: staging.MaxArchive}); err != nil {
				return err
			}
		case "entries":
			file, err := repo.LoadStaging(ctx, sourceKey)
			if err != nil {
				return err
			}
			err = repo.SaveStaging(
				ctx,
				repository.StagingWrite{Key: key, Source: file},
			)
			if err := errors.Join(err, file.Close()); err != nil {
				return err
			}
		}
	}
	return nil
}

func injectStagingMergeResult(
	r *http.Request,
	repo *repository.Repository,
	scenario string,
) error {
	id, err := strconv.ParseInt(
		path.Base(
			strings.TrimSuffix(
				r.URL.Path,
				"/result",
			),
		),
		10,
		64,
	)
	if err != nil {
		return err
	}
	return injectStagingMergeBudget(
		r.Context(),
		repo,
		id,
		scenario,
	)
}

func TestDeploymentStagingMergeBudgetAPIE2E(t *testing.T) {
	for _, scenario := range []string{"bytes", "entries", "outputs"} {
		t.Run(scenario, func(t *testing.T) {
			var repo *repository.Repository
			var once sync.Once
			var injectionErr error
			s := newStagingE2EWithTransfers(
				t,
				"",
				func(next http.Handler) http.Handler {
					return http.HandlerFunc(
						func(w http.ResponseWriter, r *http.Request) {
							if strings.HasSuffix(r.URL.Path, "/result") {
								once.Do(func() {
									injectionErr = injectStagingMergeResult(
										r,
										repo,
										scenario,
									)
								})
								if injectionErr != nil {
									t.Error(injectionErr)
									http.Error(w, "fixture failed", 500)
									return
								}
							}
							next.ServeHTTP(w, r)
						},
					)
				},
			)
			repo = s.f.repo
			var project, environment, release, deployment struct{ ID int64 }
			decode := func(data []byte, into any) {
				t.Helper()
				if err := json.Unmarshal(data, into); err != nil {
					t.Fatal(err)
				}
			}
			decode(
				s.api(
					t,
					"POST",
					"/projects",
					map[string]string{"name": "merge-budget-" + scenario},
					201,
				),
				&project,
			)
			decode(
				s.api(
					t,
					"POST",
					"/environments",
					map[string]string{"name": "merge-budget-" + scenario},
					201,
				),
				&environment,
			)
			for _, id := range []string{"test-agent", "second-agent"} {
				s.api(
					t,
					"POST",
					"/admin/agents/"+id+"/environments",
					map[string]int64{"environment_id": environment.ID},
					204,
				)
			}
			base := fmt.Sprintf("/projects/%d", project.ID)
			script := "echo producer-ok"
			if scenario == "entries" {
				script = `for ((n=0; n<20000; n++)); do : > "$DURPDEPLOY_STAGE_DIR/f$n"; done; echo producer-ok`
			}
			s.api(
				t,
				"POST",
				base+"/steps",
				map[string]any{
					"name":                 "budget producer",
					"script_body":          script,
					"interpreter":          "bash",
					"execution_target":     "agent",
					"agent_execution_mode": "host",
					"timeout_seconds":      120,
				},
				201,
			)
			s.api(
				t,
				"POST",
				base+"/steps",
				map[string]any{
					"name":             "blocked downstream",
					"script_body":      "echo downstream-must-not-run",
					"interpreter":      "bash",
					"execution_target": "local",
					"container_image":  "harbor.durp.info/dockerhub/library/bash:5.2",
				},
				201,
			)
			decode(
				s.api(
					t,
					"POST",
					base+"/releases",
					map[string]string{"version": "v1"},
					201,
				),
				&release,
			)
			decode(
				s.api(
					t,
					"POST",
					base+"/deployments",
					map[string]int64{
						"release_id":     release.ID,
						"environment_id": environment.ID,
					},
					201,
				),
				&deployment,
			)
			s.assertMergeBudgetRejected(t, scenario, deployment.ID)
		})
	}
}

func (s *stagingE2E) assertMergeBudgetRejected(
	t *testing.T,
	scenario string,
	deploymentID int64,
) {
	t.Helper()
	s.wait(t, deploymentID, "failed")
	logs := string(
		s.api(
			t,
			"GET",
			fmt.Sprintf("/deployments/%d/logs.txt", deploymentID),
			nil,
			200,
		),
	)
	if !strings.Contains(logs, staging.ErrMergeBudget.Error()) ||
		strings.Contains(logs, "downstream-must-not-run") {
		t.Fatalf("budget contract: %s", logs)
	}
	s.api(
		t,
		"GET",
		fmt.Sprintf("/deployments/%d", deploymentID),
		nil,
		200,
	)
	s.assertTransferPurged(t, scenario, deploymentID)
	s.assertRuntimeStagingPurged(t)
}
