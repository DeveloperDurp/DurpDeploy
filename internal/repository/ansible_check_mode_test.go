package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestAnsibleCheckModeHistoryAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		legacyApply := ansibleHistoryDeployment(t, repo, sql.NullInt64{}, false)
		// The explicit apply capture must override a legacy frozen step flag.
		apply := ansibleHistoryDeployment(
			t,
			repo,
			AnsibleCheckMode(false),
			true,
		)
		var check db.Deployment
		for _, mode := range []sql.NullInt64{AnsibleCheckMode(true), {}} {
			for range 6 {
				check = ansibleHistoryDeployment(t, repo, mode, true)
			}
		}
		latest, err := repo.Queries.GetLatestSuccessfulDeploymentForReleaseEnv(
			t.Context(),
			db.GetLatestSuccessfulDeploymentForReleaseEnvParams{
				ReleaseID: 1, EnvironmentID: 1,
			},
		)
		if err != nil || latest.ID != apply.ID {
			t.Fatal("check satisfied promotion history", latest.ID, err)
		}
		latest, err = repo.Queries.GetLatestSuccessfulDeploymentForProjectEnv(
			t.Context(),
			db.GetLatestSuccessfulDeploymentForProjectEnvParams{
				ProjectID: 1, EnvironmentID: 1,
			},
		)
		if err != nil || latest.ID != apply.ID {
			t.Fatal("check became current deployed version", latest.ID, err)
		}
		assertAnsibleHistoryPanels(t, repo, apply.ID, check.ID)
		assertAnsibleLegacyHistory(t, repo, apply.ID, legacyApply.ID)
		if _, err := repo.DB.ExecContext(t.Context(), "UPDATE deployments SET status = 'failed' WHERE id IN (?, ?)", apply.ID, legacyApply.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Queries.GetLatestSuccessfulDeploymentForReleaseEnv(t.Context(), db.GetLatestSuccessfulDeploymentForReleaseEnvParams{
			ReleaseID: 1, EnvironmentID: 1,
		}); !errors.Is(
			err,
			sql.ErrNoRows,
		) {
			t.Fatal("check-only success satisfied promotion", err)
		}
	})
}

func assertAnsibleHistoryPanels(
	t *testing.T,
	repo *Repository,
	applyID, checkID int64,
) {
	t.Helper()
	panels, err := repo.Queries.ListProjectPanelDeployments(
		t.Context(),
		db.ListProjectPanelDeploymentsParams{
			ProjectIds: []int64{1}, EnvironmentIds: []int64{1},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range panels {
		found = found || row.Deployment.ID == applyID
	}
	if !found {
		t.Fatal("recent checks displaced actual deployment from panel")
	}
	rows, err := repo.Queries.ListDashboardDeployments(
		t.Context(),
		db.ListDashboardDeploymentsParams{
			IsAdmin: 1, Section: "attention", RowLimit: 20,
		},
	)
	if err != nil {
		t.Fatal("attention rows", err)
	}
	active, err := repo.Queries.ListDashboardDeployments(
		t.Context(),
		db.ListDashboardDeploymentsParams{
			IsAdmin: 1, Section: "active", RowLimit: 20,
		},
	)
	if err != nil {
		t.Fatal("active rows", err)
	}
	for _, row := range append(rows, active...) {
		if row.ID == checkID {
			t.Fatal("check surfaced on dashboard", row.ID)
		}
	}
	if _, err := repo.PreviewRollback(t.Context(), checkID); !errors.Is(
		err,
		ErrRollbackConflict,
	) {
		t.Fatal("check accepted as rollback source", err)
	}
}

func assertAnsibleLegacyHistory(
	t *testing.T,
	repo *Repository,
	applyID, legacyApplyID int64,
) {
	t.Helper()
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: 1, Version: "rollback-source", StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	source, err := repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: 1, Status: "failed", AnsibleCheckMode: AnsibleCheckMode(false),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	target, err := repo.Queries.GetRollbackTarget(t.Context(), source.ID)
	if err != nil || target.ID != applyID {
		t.Fatal("legacy check became rollback target", target.ID, err)
	}
	if _, err := repo.DB.ExecContext(t.Context(), "UPDATE deployments SET status = 'failed' WHERE id = ?", applyID); err != nil {
		t.Fatal(err)
	}
	latest, err := repo.Queries.GetLatestSuccessfulDeploymentForReleaseEnv(
		t.Context(),
		db.GetLatestSuccessfulDeploymentForReleaseEnvParams{
			ReleaseID: 1, EnvironmentID: 1,
		},
	)
	if err != nil || latest.ID != legacyApplyID {
		t.Fatal("legacy apply was excluded from history", err)
	}
}

func ansibleHistoryDeployment(
	t *testing.T, repo *Repository, mode sql.NullInt64, legacyCheck bool,
) db.Deployment {
	t.Helper()
	deployment, err := repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: 1, EnvironmentID: 1, Status: "pending", AnsibleCheckMode: mode,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(map[string]any{
		"check": legacyCheck, "playbook": `- debug: {msg: '{"check":true}'}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.CreateDeploymentStep(t.Context(), db.CreateDeploymentStepParams{
		DeploymentID: deployment.ID, Kind: "ansible", Name: "Historical step",
		ExecutionTarget: "local", NetworkMode: "none",
		AnsibleJson: string(config), ContainerImage: "example/ansible:1", VariableNames: "[]",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Queries.UpdateDeploymentStatus(t.Context(), db.UpdateDeploymentStatusParams{
		ID: deployment.ID, Status: "succeeded",
	}); err != nil {
		t.Fatal(err)
	}
	return deployment
}
