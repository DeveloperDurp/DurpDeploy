//go:build e2e

package api_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type logHistoryTarget struct {
	DeploymentID int64
	APIPath      string
	WebPath      string
	Component    string
}

func (f *artifactE2E) assertLogByteBudget(
	t *testing.T,
	target logHistoryTarget,
) {
	t.Helper()
	deploymentID, apiPath, webPath := target.DeploymentID, target.APIPath, target.WebPath
	// Given: large Unicode and escaped lines plus a final lifecycle event.
	line := strings.Repeat("界😀\t\"<&", 3000) + "complete-tail"
	blank, err := f.h.repo.Queries.CreateDeploymentLog(t.Context(),
		db.CreateDeploymentLogParams{DeploymentID: deploymentID})
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[int64]bool)
	for range 80 {
		row, err := f.h.repo.Queries.CreateDeploymentLog(t.Context(),
			db.CreateDeploymentLogParams{
				DeploymentID: deploymentID, Line: line,
			})
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
	// When: the public API pages through byte-limited history.
	seen := make(map[int64]bool)
	blankSeen := false
	before := int64(0)
	for {
		path := apiPath + "/page"
		if before != 0 {
			path += fmt.Sprintf("?before=%d", before)
		}
		body := f.api(t, "GET", path, nil, 200)
		var page repository.LogPage
		decodeStepLogTest(t, body, &page)
		// Then: actual JSON bytes fit the cap, and cursors never skip IDs.
		if len(body) > repository.LogPageByteLimit || len(page.Items) == 0 ||
			page.ByteLimit != repository.LogPageByteLimit {
			t.Fatalf(
				"invalid API byte budget: bytes=%d rows=%d",
				len(body),
				len(page.Items),
			)
		}
		if before == 0 && page.LastID != last.ID {
			t.Fatal("latest cursor lost terminal output")
		}
		for _, row := range page.Items {
			if row.ID == blank.ID {
				if row.Line != "" || row.Truncated {
					t.Fatal("history changed blank output")
				}
				blankSeen = true
			}
			if expected[row.ID] {
				if seen[row.ID] || !row.Truncated ||
					len(row.Line) > repository.LogLineByteLimit ||
					!utf8.ValidString(
						row.Line,
					) || strings.Contains(row.Line, "complete-tail") {
					t.Fatal("invalid long-line preview or duplicate cursor")
				}
				seen[row.ID] = true
			}
		}
		if page.NextBefore == 0 {
			break
		}
		if page.NextBefore != page.Items[0].ID {
			t.Fatal("older cursor does not match the earliest displayed row")
		}
		before = page.NextBefore
	}
	if len(seen) != len(expected) || !blankSeen {
		t.Fatal("byte pagination omitted persisted rows")
	}
	for _, body := range []string{
		string(f.api(t, "GET", apiPath, nil, 200)),
		string(f.api(t, "GET", apiPath+".txt", nil, 200)),
		f.web(t, "GET", webPath+"/logs.txt", nil, 200),
	} {
		if strings.Count(body, "complete-tail") != len(expected) {
			t.Fatal("full-history endpoint or export shortened stored lines")
		}
	}
	if html := f.web(t, "GET", webPath, nil, 200); strings.Contains(
		html,
		"complete-tail",
	) ||
		!strings.Contains(html, "terminal-byte-marker") {
		t.Fatal("web history did not preview lines or preserve final output")
	}
	previewPath := apiPath + "/stream?format=structured&preview=true"
	previews := f.readStepLogs(t, previewPath, "")
	shortened := 0
	blankSeen = false
	for _, row := range previews {
		if row.ID == blank.ID {
			if row.Line != "" || row.Truncated {
				t.Fatal("preview stream changed blank output")
			}
			blankSeen = true
		}
		if expected[row.ID] {
			if !row.Truncated || len(row.Line) > repository.LogLineByteLimit ||
				!utf8.ValidString(row.Line) {
				t.Fatal("stream did not bound display text")
			}
			shortened++
		}
	}
	if shortened != len(expected) || !blankSeen ||
		previews[len(previews)-1].ID != last.ID {
		t.Fatal("preview replay lost rows or terminal output")
	}
	// The full structured stream still returns the complete latest long line.
	full := f.readStepLogs(t, apiPath+"/stream?format=structured",
		fmt.Sprint(last.ID-2))
	if len(full) != 2 || full[0].Line != line || full[0].Truncated ||
		full[1].ID != last.ID {
		t.Fatal("full structured stream changed its output contract")
	}
	resumed := f.readStepLogs(t, previewPath, fmt.Sprint(last.ID-1))
	if len(resumed) != 1 || resumed[0].ID != last.ID {
		t.Fatal("preview reconnection did not honor exact persisted IDs")
	}
	f.api(t, "GET", apiPath+"/stream?format=structured&preview=bad", nil, 400)
	f.web(
		t,
		"GET",
		webPath+"/logs/stream?format=structured&preview=bad",
		nil,
		400,
	)
}
