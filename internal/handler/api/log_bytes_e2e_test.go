//go:build e2e

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type logHistoryTarget struct {
	DeploymentID int64
	APIPath      string
	WebPath      string
	Component    string
}

type logByteScenario struct {
	blankID  int64
	lastID   int64
	longLine string
	longIDs  map[int64]bool
}

func (f *artifactE2E) assertLogByteBudget(
	t *testing.T,
	target logHistoryTarget,
) {
	t.Helper()
	// Given: large Unicode and escaped lines plus a final lifecycle event.
	scenario := f.seedLogByteScenario(t, target.DeploymentID)
	// When / Then: previews stay bounded and full output remains available.
	f.assertLogBytePages(t, target, scenario)
	f.assertLogByteExports(t, target, scenario)
	f.assertLogByteStreams(t, target, scenario)
	f.api(
		t,
		"GET",
		target.APIPath+"/stream?format=structured&preview=bad",
		nil,
		400,
	)
	f.web(
		t,
		"GET",
		target.WebPath+"/logs/stream?format=structured&preview=bad",
		nil,
		400,
	)
}

func (f *artifactE2E) seedLogByteScenario(
	t *testing.T,
	deploymentID int64,
) logByteScenario {
	t.Helper()
	line := strings.Repeat("界😀\t\"<&", 3000) + "complete-tail"
	blank, err := f.h.repo.Queries.CreateDeploymentLog(t.Context(),
		db.CreateDeploymentLogParams{DeploymentID: deploymentID})
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[int64]bool)
	for range 80 {
		row, err := f.h.repo.Queries.CreateDeploymentLog(
			t.Context(),
			db.CreateDeploymentLogParams{
				DeploymentID: deploymentID,
				Line:         line,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		expected[row.ID] = true
	}
	last, err := f.h.repo.Queries.CreateStepDeploymentLog(t.Context(),
		db.CreateStepDeploymentLogParams{
			DeploymentID: deploymentID, Line: "terminal-byte-marker",
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.Queries.UpdateDeploymentStatus(t.Context(),
		db.UpdateDeploymentStatusParams{
			ID: deploymentID, Status: "succeeded",
		}); err != nil {
		t.Fatal(err)
	}
	return logByteScenario{
		blankID: blank.ID, lastID: last.ID, longLine: line, longIDs: expected,
	}
}

func (f *artifactE2E) assertLogBytePages(
	t *testing.T,
	target logHistoryTarget,
	scenario logByteScenario,
) {
	t.Helper()
	seen := make(map[int64]bool)
	blankSeen := false
	before := int64(0)
	for {
		page := f.readLogBytePage(t, target.APIPath, before)
		if before == 0 && page.LastID != scenario.lastID {
			t.Fatal("latest cursor lost terminal output")
		}
		foundBlank := scenario.assertPageRows(t, page.Items, seen)
		blankSeen = blankSeen || foundBlank
		if page.NextBefore == 0 {
			break
		}
		if page.NextBefore != page.Items[0].ID {
			t.Fatal("older cursor does not match the earliest displayed row")
		}
		before = page.NextBefore
	}
	if len(seen) != len(scenario.longIDs) || !blankSeen {
		t.Fatal("byte pagination omitted persisted rows")
	}
}

func (f *artifactE2E) readLogBytePage(
	t *testing.T,
	apiPath string,
	before int64,
) repository.LogPage {
	t.Helper()
	path := apiPath + "/page"
	if before != 0 {
		path += fmt.Sprintf("?before=%d", before)
	}
	body := f.api(t, "GET", path, nil, 200)
	var page repository.LogPage
	decodeStepLogTest(t, body, &page)
	if len(body) > repository.LogPageByteLimit || len(page.Items) == 0 ||
		page.ByteLimit != repository.LogPageByteLimit {
		t.Fatalf(
			"invalid API byte budget: bytes=%d rows=%d",
			len(body),
			len(page.Items),
		)
	}
	return page
}
