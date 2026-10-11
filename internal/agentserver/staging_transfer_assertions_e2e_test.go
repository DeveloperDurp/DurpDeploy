//go:build e2e && agentstaging

package agentserver_test

import (
	"fmt"
	agentexec "github.com/DeveloperDurp/durpdeploy-agent/executor"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func assertDetachedStagingChildren(t *testing.T, logs string) {
	t.Helper()
	groups := regexp.MustCompile(`detached-group=([0-9]+)`).
		FindAllStringSubmatch(logs, -1)
	for _, group := range groups {
		pid, err := strconv.Atoi(group[1])
		if err != nil || pid <= 0 {
			t.Fatal("invalid detached group", group)
		}
		t.Cleanup(func() { agentexec.KillProcessGroup(pid) })
	}
	children := regexp.MustCompile(`detached-child=([0-9]+)`).
		FindAllStringSubmatch(logs, -1)
	if len(children) != 2 || len(groups) != 2 {
		t.Fatalf("missing detached child evidence: %s", logs)
	}
	for _, child := range children {
		_, err := os.Stat("/proc/" + child[1] + "/stat")
		if !os.IsNotExist(err) {
			t.Fatalf(
				"completed deployment retained detached child: %s %v",
				child[1],
				err,
			)
		}
	}
}

func (s *stagingE2E) assertTransferPurged(
	t *testing.T,
	scenario string,
	deploymentID int64,
) {
	t.Helper()
	if data := string(
		s.api(
			t,
			"GET",
			fmt.Sprintf("/deployments/%d/staging", deploymentID),
			nil,
			200,
		),
	); strings.TrimSpace(
		data,
	) != "[]" {
		t.Fatalf("staging retained: %s", data)
	}
	movedRoots := 0
	for _, directory := range s.directories {
		entries, err := os.ReadDir(filepath.Join(directory, "staging"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("staging directory retained: %v %v", entries, err)
		}
		if scenario == "renamed-root" ||
			scenario == "escaped-root-workspace" {
			entries, err := os.ReadDir(
				filepath.Join(directory, "retained"),
			)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil || len(entries) != 0 {
				t.Fatalf(
					"renamed staging root retained files: %v %v",
					entries,
					err,
				)
			}
			movedRoots++
		}
	}
	if (scenario == "renamed-root" || scenario == "escaped-root-workspace") &&
		movedRoots == 0 {
		t.Fatal("agents did not rename the staging root")
	}
}
