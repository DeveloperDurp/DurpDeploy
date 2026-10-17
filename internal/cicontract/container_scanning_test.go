package cicontract

import (
	"slices"
	"strings"
	"testing"
)

func TestContainerChecksHaveNoPublishingAuthority(t *testing.T) {
	security := readWorkflow(t, "security")
	if len(security.Permissions) != 1 ||
		security.Permissions["contents"] != "read" {
		t.Fatal("security scans must have read-only contents permission")
	}
	for _, name := range []string{"containers", "image-rescan"} {
		job, ok := security.Jobs[name]
		if !ok || job.ContinueOnError || job.TimeoutMinutes != 30 {
			t.Fatalf("%s must be bounded and blocking", name)
		}
		if len(job.Permissions) != 0 {
			t.Fatalf("%s must inherit read-only permissions", name)
		}
		for _, step := range job.Steps {
			if strings.Contains(step.Run, "secrets.") ||
				step.With["push"] == "true" ||
				strings.Contains(step.Uses, "login-action") {
				t.Fatalf("%s must not receive publishing authority", name)
			}
		}
	}
	job := security.Jobs["containers"]
	if job.If != "" {
		t.Fatal("configuration and dependencies must run on every event")
	}
	for _, command := range []string{
		"make trivy-tools",
		"make container-scan-test",
		"bash scripts/container_scan.sh config . artifacts/container/config",
		"bash scripts/container_scan.sh dependencies . artifacts/container/dependencies",
		"bash scripts/check-agent-compose-contract.sh",
	} {
		found := false
		for _, step := range job.Steps {
			found = found || step.Run == command
		}
		if !found {
			t.Errorf("missing required container gate: %s", command)
		}
	}
	rescan := security.Jobs["image-rescan"]
	if !strings.Contains(rescan.If, "github.workflow == 'Security gates'") ||
		!strings.Contains(rescan.If, "github.event_name == 'schedule'") ||
		!strings.Contains(
			rescan.If,
			"github.event_name == 'workflow_dispatch'",
		) {
		t.Fatal("fresh image builds must run on standalone schedule/dispatch")
	}
	for _, event := range []string{"schedule", "workflow_dispatch"} {
		if _, ok := security.On[event]; !ok {
			t.Errorf("image rescans lack %s trigger", event)
		}
	}
}

func TestImageEvidenceCannotPromoteFailedScans(t *testing.T) {
	release := readWorkflow(t, "release")
	images := release.Jobs["images"]
	if images.TimeoutMinutes != 30 {
		t.Fatal("production image checks must have an explicit timeout")
	}
	scan, evidence, source, archive := -1, -1, -1, -1
	for index, step := range images.Steps {
		switch step.Name {
		case "Scan image archive":
			scan = index
		case "Retain safe image evidence":
			evidence = index
			if !strings.Contains(step.If, "always()") ||
				step.With["path"] != "${{ runner.temp }}/image-evidence/" ||
				step.With["retention-days"] != "7" {
				t.Fatal("failed scans must retain only sanitized evidence")
			}
		case "Record verified source":
			source = index
			if step.If != "" {
				t.Fatal("source verification must require scan success")
			}
		case "Retain scanned image":
			archive = index
			if step.If != "" ||
				step.With["path"] != "${{ runner.temp }}/release-image/" {
				t.Fatal("failed images must never become promotion artifacts")
			}
		}
	}
	if scan < 0 || evidence <= scan || source <= scan || archive <= source {
		t.Fatal("scan evidence must precede successful source/image retention")
	}
	if !slices.Contains(release.Jobs["image-verification"].Needs, "security") {
		t.Fatal(
			"PR image gate must also require configuration/dependency scans",
		)
	}
}
