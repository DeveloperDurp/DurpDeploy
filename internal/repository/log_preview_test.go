package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"durpdeploy/internal/db"
)

func TestLogPreviewText(t *testing.T) {
	for i, value := range []string{
		"short\x00complete", strings.Repeat("a", LogLineByteLimit),
		strings.Repeat("界😀", 4000),
		strings.Repeat("a", LogLineByteLimit-1) + "界",
		strings.Repeat("\xffa", LogLineByteLimit),
	} {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			// Given: arbitrary persisted UTF-8 or invalid bytes.
			wantShortened := len(value) > LogLineByteLimit ||
				!utf8.ValidString(value)
			// When: the display preview is produced.
			text, shortened := previewText(value, LogLineByteLimit)
			// Then: previews fit the byte cap and untouched text remains exact.
			if !utf8.ValidString(text) || len(text) > LogLineByteLimit ||
				shortened != wantShortened || (!shortened && text != value) {
				t.Fatalf(
					"invalid preview: bytes=%d shortened=%t",
					len(text),
					shortened,
				)
			}
		})
	}
}

const logPreviewRowCount = 80

func TestLogByteBudgetsAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		deployment, err := repo.Queries.CreateDeployment(t.Context(),
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "succeeded",
			})
		if err != nil {
			t.Fatal(err)
		}
		// Given: escaped Unicode output exceeds a page's byte budget.
		line := strings.Repeat("界😀\t\"<&", 3000) + "complete-tail"
		if name == "SQLite" {
			line = "before\x00after" + line
		}
		log := db.CreateStepDeploymentLogParams{
			DeploymentID: deployment.ID, Line: line,
		}
		seedLargeLogPreviews(t, repo, log)
		// When / Then: history and live previews preserve their bounds.
		assertLogBytePages(t, repo, deployment.ID)
		assertLogPreviewBatch(t, repo, log)
	})
}

func seedLargeLogPreviews(
	t *testing.T,
	repo *Repository,
	log db.CreateStepDeploymentLogParams,
) {
	t.Helper()
	for i := range logPreviewRowCount {
		log.StepName = sql.NullString{Valid: i%2 == 0}
		if _, err := repo.Queries.CreateStepDeploymentLog(t.Context(), log); err != nil {
			t.Fatal(err)
		}
	}
}

func assertLogBytePages(t *testing.T, repo *Repository, deploymentID int64) {
	t.Helper()
	seen := make(map[int64]bool)
	before := int64(0)
	for {
		page, err := repo.ReadLogPage(t.Context(), deploymentID, before)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded)+1 > LogPageByteLimit || len(page.Items) == 0 {
			t.Fatalf(
				"invalid page: bytes=%d rows=%d",
				len(encoded),
				len(page.Items),
			)
		}
		assertLogPreviewPageRows(t, page, seen)
		if page.NextBefore == 0 {
			break
		}
		if page.NextBefore != page.Items[0].ID {
			t.Fatal("byte-limited cursor skipped output")
		}
		before = page.NextBefore
	}
	if len(seen) != logPreviewRowCount {
		t.Fatalf("read %d of %d persisted rows", len(seen), logPreviewRowCount)
	}
}

func assertLogPreviewPageRows(t *testing.T, page LogPage, seen map[int64]bool) {
	t.Helper()
	for i, row := range page.Items {
		if seen[row.ID] || !row.Truncated || !utf8.ValidString(row.Line) ||
			len(row.Line) > LogLineByteLimit || row.StepState.Valid ||
			(i > 0 && page.Items[i-1].ID >= row.ID) {
			t.Fatalf("invalid preview row %d", row.ID)
		}
		seen[row.ID] = true
	}
}

func assertLogPreviewBatch(
	t *testing.T,
	repo *Repository,
	log db.CreateStepDeploymentLogParams,
) {
	t.Helper()
	previews, err := repo.ReadLogPreviewsAfter(t.Context(), log.DeploymentID, 0)
	if err != nil || len(previews) != LogPreviewBatchLimit {
		t.Fatalf("preview batch: rows=%d err=%v", len(previews), err)
	}
	for i, preview := range previews {
		if preview.StepName.Valid != (i%2 == 0) ||
			!strings.HasPrefix(preview.Line, log.Line[:16]) {
			t.Fatal("preview lost nullability, NUL bytes, or Unicode prefix")
		}
		original, err := repo.Queries.GetDeploymentLog(t.Context(), preview.ID)
		if err != nil || original.Line != log.Line {
			t.Fatalf("persisted output changed: %v", err)
		}
	}
}

func TestEmptyLogPreviewsAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		deployment, err := repo.Queries.CreateDeployment(t.Context(),
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "succeeded",
			})
		if err != nil {
			t.Fatal(err)
		}
		// Given: ordinary blank output is persisted without truncation.
		row, err := repo.Queries.CreateDeploymentLog(t.Context(),
			db.CreateDeploymentLogParams{DeploymentID: deployment.ID})
		if err != nil {
			t.Fatal(err)
		}
		// When: history and live previews read the blank line.
		page, err := repo.ReadLogPage(t.Context(), deployment.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		live, err := repo.ReadLogPreviewsAfter(t.Context(), deployment.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		// Then: both surfaces retain its ID and exact empty text.
		for _, previews := range [][]LogPreview{page.Items, live} {
			if len(previews) != 1 || previews[0].ID != row.ID ||
				previews[0].Line != "" || previews[0].Truncated {
				t.Fatalf("blank preview changed: %+v", previews)
			}
		}
	})
}
