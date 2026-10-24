package repository

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTemplateLibraryTerraformRejectsLocalState(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"config", "stage", "scratch", "bin"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeLibraryFixture(
		t,
		filepath.Join(root, "config", ".terraform.lock.hcl"),
		"# fixture\n",
		0600,
	)
	log := filepath.Join(root, "calls")
	writeLibraryFixture(t, filepath.Join(root, "bin", "terraform"), `#!/bin/sh
printf '%s\n' "$*" >> "$CALL_LOG"
if [ "$1" = init ]; then
    mkdir .terraform
    printf '%s' '{"backend":{"type":"local"}}' > .terraform/terraform.tfstate
fi
`, 0700)
	entry, err := GetLibraryTemplate("terraform-plan-apply")
	if err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + filepath.Join(root, "bin") + ":" + os.Getenv("PATH"),
		"CALL_LOG=" + log, "TMPDIR=" + filepath.Join(root, "scratch"),
		"TF_CONFIG_DIR=" + filepath.Join(root, "config"),
		"DURPDEPLOY_STAGE_DIR=" + filepath.Join(root, "stage"),
		"DURPDEPLOY_APPROVED_DIR=" + filepath.Join(root, "missing-approval"),
	}
	runLibraryScript(t, entry.Snapshot.Steps[0].ScriptBody, env, false)
	runLibraryScript(t, entry.Snapshot.Steps[1].ScriptBody, env, false)
	commands, err := os.ReadFile(log)
	if err != nil ||
		string(commands) != "init -input=false -no-color -lockfile=readonly\n" {
		t.Fatal(
			"local state or missing approval reached plan/apply",
			string(commands),
			err,
		)
	}
	files, err := os.ReadDir(filepath.Join(root, "stage"))
	if err != nil || len(files) != 0 {
		t.Fatal("failed plan exported an approval context", err)
	}
	scratch, err := os.ReadDir(filepath.Join(root, "scratch"))
	if err != nil || len(scratch) != 0 {
		t.Fatal("failed plan left executable scratch files", err)
	}
}

func TestTemplateLibraryTerraformRejectsExistingContext(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config")
	stage := filepath.Join(root, "stage")
	stale := filepath.Join(stage, "terraform-context")
	scratch := filepath.Join(root, "scratch")
	for _, directory := range []string{config, stage, stale, scratch} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeLibraryFixture(
		t,
		filepath.Join(config, ".terraform.lock.hcl"),
		"# fixture",
		0600,
	)
	writeLibraryFixture(t, filepath.Join(stale, "tfplan"), "old plan", 0600)
	writeLibraryFixture(
		t,
		filepath.Join(stale, "review.json"),
		"old review",
		0600,
	)
	log := filepath.Join(root, "calls")
	writeLibraryFixture(t, filepath.Join(root, "terraform"), `#!/bin/sh
printf '%s\n' "$*" >> "$CALL_LOG"
case "$1" in
    init) mkdir .terraform; printf '%s' '{"backend":{"type":"http"}}' > .terraform/terraform.tfstate ;;
    plan) printf '%s' 'new plan' > tfplan ;;
    show) printf '%s' '{}' ;;
esac
`, 0700)
	entry, err := GetLibraryTemplate("terraform-plan-apply")
	if err != nil {
		t.Fatal(err)
	}
	runLibraryScript(t, entry.Snapshot.Steps[0].ScriptBody, []string{
		"PATH=" + root + ":" + os.Getenv("PATH"), "CALL_LOG=" + log,
		"TMPDIR=" + scratch, "TF_CONFIG_DIR=" + config,
		"DURPDEPLOY_STAGE_DIR=" + stage,
	}, false)
	for name, expected := range map[string]string{"tfplan": "old plan", "review.json": "old review"} {
		data, err := os.ReadFile(filepath.Join(stale, name))
		if err != nil || string(data) != expected {
			t.Fatal("rejected plan changed the existing context", name, err)
		}
	}
	files, err := os.ReadDir(stale)
	if err != nil || len(files) != 2 {
		t.Fatal("plan nested new artifacts beneath the existing context", err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("existing context reached Terraform execution", err)
	}
	files, err = os.ReadDir(scratch)
	if err != nil || len(files) != 0 {
		t.Fatal("rejected plan left executable scratch files", err)
	}
}
