//go:build e2e

package api_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"durpdeploy/internal/repository"
)

func assertBlankLogText(t *testing.T, line string, truncated bool) {
	t.Helper()
	if line != "" || truncated {
		t.Fatal("preview changed blank output")
	}
}

func assertShortenedLogText(t *testing.T, line string, truncated bool) {
	t.Helper()
	if !truncated || len(line) > repository.LogLineByteLimit ||
		!utf8.ValidString(line) {
		t.Fatal("invalid long-line preview")
	}
}

func (scenario logByteScenario) assertPageRows(
	t *testing.T,
	rows []repository.LogPreview,
	seen map[int64]bool,
) bool {
	t.Helper()
	blankSeen := false
	for _, row := range rows {
		if row.ID == scenario.blankID {
			assertBlankLogText(t, row.Line, row.Truncated)
			blankSeen = true
		}
		if !scenario.longIDs[row.ID] {
			continue
		}
		if seen[row.ID] || strings.Contains(row.Line, "complete-tail") {
			t.Fatal("invalid long-line preview or duplicate cursor")
		}
		assertShortenedLogText(t, row.Line, row.Truncated)
		seen[row.ID] = true
	}
	return blankSeen
}

func (f *artifactE2E) assertLogByteExports(
	t *testing.T,
	target logHistoryTarget,
	scenario logByteScenario,
) {
	t.Helper()
	for _, body := range []string{
		string(f.api(t, "GET", target.APIPath, nil, 200)),
		string(f.api(t, "GET", target.APIPath+".txt", nil, 200)),
		f.web(t, "GET", target.WebPath+"/logs.txt", nil, 200),
	} {
		if strings.Count(body, "complete-tail") != len(scenario.longIDs) {
			t.Fatal("full-history endpoint or export shortened stored lines")
		}
	}
	if html := f.web(t, "GET", target.WebPath, nil, 200); strings.Contains(
		html, "complete-tail",
	) || !strings.Contains(html, "terminal-byte-marker") {
		t.Fatal("web history did not preview lines or preserve final output")
	}
}

func (f *artifactE2E) assertLogByteStreams(
	t *testing.T,
	target logHistoryTarget,
	scenario logByteScenario,
) {
	t.Helper()
	previewPath := target.APIPath + "/stream?format=structured&preview=true"
	f.assertLogPreviewReplay(t, previewPath, scenario)
	// The full structured stream still returns the complete latest long line.
	full := f.readStepLogs(t, target.APIPath+"/stream?format=structured",
		fmt.Sprint(scenario.lastID-2))
	if len(full) != 2 || full[0].Line != scenario.longLine ||
		full[0].Truncated ||
		full[1].ID != scenario.lastID {
		t.Fatal("full structured stream changed its output contract")
	}
	resumed := f.readStepLogs(t, previewPath, fmt.Sprint(scenario.lastID-1))
	if len(resumed) != 1 || resumed[0].ID != scenario.lastID {
		t.Fatal("preview reconnection did not honor exact persisted IDs")
	}
}

func (f *artifactE2E) assertLogPreviewReplay(
	t *testing.T,
	previewPath string,
	scenario logByteScenario,
) {
	t.Helper()
	previews := f.readStepLogs(t, previewPath, "")
	shortened := 0
	blankSeen := false
	for _, row := range previews {
		if row.ID == scenario.blankID {
			assertBlankLogText(t, row.Line, row.Truncated)
			blankSeen = true
		}
		if scenario.longIDs[row.ID] {
			assertShortenedLogText(t, row.Line, row.Truncated)
			shortened++
		}
	}
	if shortened != len(scenario.longIDs) || !blankSeen ||
		previews[len(previews)-1].ID != scenario.lastID {
		t.Fatal("preview replay lost rows or terminal output")
	}
}
