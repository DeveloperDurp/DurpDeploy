package repository

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/stepdefinition"
	"golang.org/x/crypto/ssh"
)

func TestAnsibleSnapshotsAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo := rotationTestRepository(t, name)
		testAnsibleSnapshotDefinitions(t, repo)
	})
}

func ansibleSnapshotDefinition(t *testing.T) stepdefinition.Definition {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return stepdefinition.Definition{
		Kind: "ansible", Name: "Ansible snapshots", ExecutionTarget: "local", ContainerImage: "example/ansible:1", NetworkMode: "bridge",
		Ansible: &stepdefinition.Ansible{
			Playbook: "- hosts: all\n  tasks: []\n", Inventory: "all:\n  hosts:\n    target: {}\n",
			SSHKeyVariable: "SSH_KEY", KnownHosts: "target " + string(ssh.MarshalAuthorizedKey(key)),
			ExtraVars: map[string]string{
				"message": "MESSAGE",
			}, Tags: []string{"deploy"}, Limit: []string{"target"},
		},
	}
}

func testAnsibleSnapshotDefinitions(t *testing.T, repo *Repository) {
	t.Helper()
	definition := ansibleSnapshotDefinition(t)
	if _, err := repo.CreateCredential(t.Context(), 1, false, CredentialCreateInput{Name: "SSH_KEY", Value: "encrypted-key-capture"}); err != nil {
		t.Fatal(err)
	}
	rotationReleaseSecret(
		t,
		repo,
		1,
		"SSH_KEY",
		"encrypted-key-capture",
		sql.NullInt64{},
		1,
	)
	rotationReleaseSecret(
		t,
		repo,
		1,
		"MESSAGE",
		"snapshot-message",
		sql.NullInt64{},
		1,
	)
	step, err := repo.CreateStepWithPlacement(
		t.Context(),
		definition.CreateStepParams(1, "[]"),
		"local",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, err := stepdefinition.FromStep(step, nil)
	if err != nil ||
		roundtrip.Ansible.JSON() != definition.Ansible.JSON() ||
		roundtrip.Interpreter != "" {
		t.Fatal("step contract did not round-trip", err)
	}
	template, err := repo.CreateStepTemplateWithPlacement(
		t.Context(),
		definition.CreateTemplateParams("[]"),
		"local",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := repo.Queries.ListStepTemplateVersions(
		t.Context(),
		template.ID,
	)
	if err != nil || len(versions) != 1 ||
		versions[0].AnsibleJson != definition.Ansible.JSON() {
		t.Fatal("template version lost Ansible configuration", err)
	}
	testAnsibleDeploymentSnapshots(t, repo, definition, step.ID)
}

func testAnsibleDeploymentSnapshots(
	t *testing.T, repo *Repository, definition stepdefinition.Definition,
	stepID int64,
) {
	t.Helper()
	config := *definition.Ansible
	check := true
	config.Check = &check
	definition.Ansible = &config
	raw, err := json.Marshal([]stepdefinition.Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.UpdateRelease(t.Context(), db.UpdateReleaseParams{ID: 1, ProjectID: 1, Version: "v1", StepsJson: string(raw)}); err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:        1,
			EnvironmentID:    1,
			Status:           "pending",
			AnsibleCheckMode: AnsibleCheckMode(true),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if deployment.Deployment.AnsibleCheckMode != AnsibleCheckMode(true) {
		t.Fatal("check mode was not captured")
	}
	fresh, err := repo.CreateDeployment(t.Context(), db.CreateDeploymentParams{
		ReleaseID: 1, EnvironmentID: 1, Status: "pending",
	})
	if err != nil ||
		fresh.Deployment.AnsibleCheckMode != AnsibleCheckMode(false) {
		t.Fatal("fresh deployment inherited check mode", err)
	}
	steps, err := repo.Queries.ListDeploymentSteps(
		t.Context(),
		deployment.Deployment.ID,
	)
	if err != nil || len(steps) != 1 || steps[0].Kind != "ansible" ||
		steps[0].AnsibleJson != definition.Ansible.JSON() {
		t.Fatal("deployment snapshot lost Ansible configuration", err)
	}
	definition.Ansible.Check = nil
	definition.Ansible.Playbook = "- hosts: all\n  tasks:\n    - debug: {msg: edited}\n"
	if _, err := repo.UpdateStepWithPlacement(t.Context(), definition.UpdateStepParams(stepID, "[]"), "local", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.UpdateRelease(t.Context(), db.UpdateReleaseParams{ID: 1, ProjectID: 1, Version: "v1", StepsJson: "[]"}); err != nil {
		t.Fatal(err)
	}
	rerun, err := repo.CreateDeploymentFromDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     1,
			EnvironmentID: 1,
			Status:        "pending",
		},
		deployment.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := repo.Queries.GetDeploymentStepSource(
		t.Context(),
		rerun.Deployment.ID,
	)
	if err != nil || frozen.StepsJson != string(raw) {
		t.Fatal("rerun lost frozen Ansible source", err)
	}
	if rerun.Deployment.AnsibleCheckMode != AnsibleCheckMode(true) {
		t.Fatal("rerun lost captured check mode")
	}
	// Model an accepted execution from before the deployment option existed.
	if _, err := repo.DB.ExecContext(t.Context(), "UPDATE deployments SET ansible_check_mode = NULL WHERE id = ?", deployment.Deployment.ID); err != nil {
		t.Fatal(err)
	}
	legacy, err := repo.CreateDeploymentFromDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: 1, EnvironmentID: 1, Status: "pending",
		},
		deployment.Deployment.ID,
	)
	if err != nil || legacy.Deployment.AnsibleCheckMode.Valid {
		t.Fatal("legacy source mode changed on rerun", err)
	}
}
