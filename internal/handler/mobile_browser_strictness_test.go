//go:build mobilebrowser

package handler_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestMobileBrowserGeometryDetectsSecondControl(t *testing.T) {
	// Given
	root := repositoryRoot(t)
	command := exec.Command("node", "scripts/mobile_readability_qa.mjs")
	command.Dir = root
	command.Env = append(os.Environ(), "MOBILE_GEOMETRY_SELF_TEST=1")

	// When
	output, err := command.CombinedOutput()

	// Then
	if err == nil {
		t.Fatal("second off-screen control did not fail strict geometry")
	}
	if !strings.Contains(string(output), "control is unreachable") {
		t.Fatalf("strict geometry failure = %s", output)
	}
}

func TestMobileBrowserMissingSelectorFails(t *testing.T) {
	// Given
	root := repositoryRoot(t)
	command := exec.Command("node", "scripts/mobile_readability_qa.mjs")
	command.Dir = root
	command.Env = append(
		os.Environ(),
		"MOBILE_MISSING_SELECTOR_SELF_TEST=1",
		"MOBILE_BASELINE=1",
	)

	// When
	output, err := command.CombinedOutput()

	// Then
	if err == nil {
		t.Fatal("missing selector did not fail strict marker checks")
	}
	if !strings.Contains(
		string(output),
		"missing-selector marker is missing at 375px",
	) {
		t.Fatalf("missing selector failure = %s", output)
	}
}

func TestMobileBrowserHarnessRejectsRemoteBaseURL(t *testing.T) {
	// Given
	const want = "BASE_URL must use a localhost origin"
	root := repositoryRoot(t)
	command := exec.Command("node", "scripts/mobile_readability_qa.mjs")
	command.Dir = root
	command.Env = append(os.Environ(), "MOBILE_BASE_URL_SELF_TEST=1")

	// When
	output, err := command.CombinedOutput()

	// Then
	if err == nil {
		t.Fatal("remote base URL did not fail harness validation")
	}
	if !strings.Contains(string(output), want) {
		t.Fatalf("remote base URL failure = %s", output)
	}
}

func TestMobileBrowserEnvironment_uses_strict_mode_without_baseline(
	t *testing.T,
) {
	// Given
	fixtures := mobileBrowserFixtures{
		serverURL: "http://example.test",
		steps:     []db.Step{{}},
	}
	session := &authedSession{sessionToken: "session-token"}

	// When
	environment := mobileBrowserEnvironment(fixtures, "viewer", session)

	// Then
	for _, value := range environment {
		if value == "MOBILE_BASELINE=1" {
			t.Fatal(
				"normal browser test environment enables diagnostic baseline",
			)
		}
		if value == "MOBILE_STRICT=1" {
			return
		}
	}
	t.Fatal("normal browser test environment does not force strict mode")
}

func TestMobileBrowserNodeModules_uses_repository_default_when_unset(
	t *testing.T,
) {
	// Given
	t.Setenv("MOBILE_BROWSER_NODE_MODULES", "")

	// When
	got := mobileBrowserNodeModules("/workspace")

	// Then
	want := "/workspace/node_modules"
	if got != want {
		t.Fatalf("node modules path = %q, want %q", got, want)
	}
}

func TestMobileBrowserNodeModules_uses_container_override_when_set(
	t *testing.T,
) {
	// Given
	t.Setenv(
		"MOBILE_BROWSER_NODE_MODULES",
		"/opt/mobile-browser/node_modules",
	)

	// When
	got := mobileBrowserNodeModules("/workspace")

	// Then
	want := "/opt/mobile-browser/node_modules"
	if got != want {
		t.Fatalf("node modules path = %q, want %q", got, want)
	}
}

func TestMobileBrowserHarness_rejects_unknown_target(t *testing.T) {
	// Given
	root := repositoryRoot(t)
	requireMobileBrowserPrerequisites(t, root)
	fixtures := mobileBrowserFixtures{
		serverURL:   "http://127.0.0.1:1",
		evidenceDir: t.TempDir(),
		steps:       []db.Step{{}},
	}
	command := exec.Command("node", "scripts/mobile_readability_qa.mjs")
	command.Dir = root
	command.Env = append(
		os.Environ(),
		mobileBrowserEnvironment(
			fixtures,
			"viewer",
			&authedSession{sessionToken: "session-token"},
		)...,
	)
	command.Env = append(command.Env, "MOBILE_TARGETS=missing-target")

	// When
	output, err := command.CombinedOutput()

	// Then
	if err == nil {
		t.Fatalf("unknown target passed: %s", output)
	}
	if !strings.Contains(string(output), "unknown mobile readability target") {
		t.Fatalf("unknown target failure = %s", output)
	}
}

func TestMobileBrowserHarnessRejectsInvalidViewportSelection(t *testing.T) {
	for _, test := range []struct {
		name string
		env  []string
	}{
		{
			name: "unknown viewport",
			env:  []string{"MOBILE_VIEWPORTS=unsupported"},
		},
		{
			name: "empty selection",
			env: []string{
				"MOBILE_VIEWPORTS=,",
				"MOBILE_TARGETS=missing-target",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given
			root := repositoryRoot(t)
			fixtures := mobileBrowserFixtures{
				serverURL:   "http://127.0.0.1:1",
				evidenceDir: t.TempDir(),
				steps:       []db.Step{{}},
			}
			command := exec.Command("node", "scripts/mobile_readability_qa.mjs")
			command.Dir = root
			command.Env = append(
				os.Environ(),
				mobileBrowserEnvironment(
					fixtures,
					"viewer",
					&authedSession{sessionToken: "session-token"},
				)...,
			)
			command.Env = append(command.Env, test.env...)

			// When
			output, err := command.CombinedOutput()

			// Then
			if err == nil {
				t.Fatal("invalid viewport selection was accepted")
			}
			if !strings.Contains(
				string(output),
				"invalid mobile readability viewport selection",
			) {
				t.Fatalf("invalid viewport failure = %s", output)
			}
		})
	}
}
