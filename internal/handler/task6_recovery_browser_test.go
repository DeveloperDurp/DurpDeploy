//go:build mobilebrowser

package handler_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTask6AddFormBrowserProbe(t *testing.T) {
	root := repositoryRoot(t)
	requireMobileBrowserPrerequisites(t, root)
	fixtures := newMobileBrowserFixtures(t)
	fixtures.repo.DB.SetMaxOpenConns(1)
	command := exec.Command(
		"node",
		".omo/evidence/task-6-recovery/add-form-probe.mjs",
	)
	command.Dir = root
	command.Env = append(
		os.Environ(),
		mobileBrowserEnvironment(
			fixtures,
			"admin",
			fixtures.sessions["admin"],
		)...,
	)
	command.Env = append(
		command.Env,
		"NODE_PATH="+mobileBrowserNodeModules(root),
		"TASK6_OUTPUT="+filepath.Join(
			root,
			".omo/evidence/task-6-recovery/browser-probe.json",
		),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("task 6 browser probe: %v\n%s", err, output)
	}
}
