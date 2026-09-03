package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"durpdeploy/internal/db"
)

func TestStepForm_DefaultZeroSortOrderIsNormalized(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("step-order")

	// Given
	stepForm := url.Values{
		"name":        {"deploy"},
		"script_body": {"echo ok"},
		"sort_order":  {"0"},
	}
	stepForm.Set("csrf_token", h.csrfToken())

	// When
	resp, err := h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/steps", h.server.URL, proj.ID),
		stepForm,
	)
	if err != nil {
		t.Fatalf("POST step: %v", err)
	}
	resp.Body.Close()

	steps, err := h.repo.Queries.ListStepsByProject(
		context.Background(),
		proj.ID,
	)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if got := steps[0].SortOrder; got != 1 {
		t.Fatalf("stored sort_order = %d", got)
	}

	releaseForm := url.Values{"version": {"1.0.0"}}
	releaseForm.Set("csrf_token", h.csrfToken())
	resp, err = h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/releases", h.server.URL, proj.ID),
		releaseForm,
	)
	if err != nil {
		t.Fatalf("POST release: %v", err)
	}
	resp.Body.Close()

	releases, err := h.repo.Queries.ListReleasesByProject(
		context.Background(),
		proj.ID,
	)
	if err != nil {
		t.Fatalf("list releases: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("expected 1 release, got %d", len(releases))
	}
	var snapshots []struct {
		SortOrder int64 `json:"sort_order"`
	}
	if err := json.Unmarshal(
		[]byte(releases[0].StepsJson),
		&snapshots,
	); err != nil {
		t.Fatalf("decode release steps: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot step, got %d", len(snapshots))
	}

	// Then
	if got := snapshots[0].SortOrder; got != 1 {
		t.Fatalf("release sort_order = %d", got)
	}
}

func TestReleaseSnapshot_normalizesGappedStepOrdersOnCreateAndRefresh(
	t *testing.T,
) {
	h := newProjectHarness(t)
	proj := h.makeProject("release-order-gap")
	ctx := context.Background()

	// Given
	first, err := h.repo.Queries.CreateStep(ctx, db.CreateStepParams{
		ProjectID:  proj.ID,
		Name:       "first",
		ScriptBody: "echo first",
		SortOrder:  10,
	})
	if err != nil {
		t.Fatalf("create first step: %v", err)
	}
	second, err := h.repo.Queries.CreateStep(ctx, db.CreateStepParams{
		ProjectID:  proj.ID,
		Name:       "second",
		ScriptBody: "echo second",
		SortOrder:  20,
	})
	if err != nil {
		t.Fatalf("create second step: %v", err)
	}
	third, err := h.repo.Queries.CreateStep(ctx, db.CreateStepParams{
		ProjectID:  proj.ID,
		Name:       "third",
		ScriptBody: "echo third",
		SortOrder:  30,
	})
	if err != nil {
		t.Fatalf("create third step: %v", err)
	}
	releaseForm := url.Values{"version": {"2.0.0"}}
	releaseForm.Set("csrf_token", h.csrfToken())

	// When
	resp, err := h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/releases", h.server.URL, proj.ID),
		releaseForm,
	)
	if err != nil {
		t.Fatalf("POST release: %v", err)
	}
	resp.Body.Close()

	// Then
	releases, err := h.repo.Queries.ListReleasesByProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("list releases: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("expected 1 release, got %d", len(releases))
	}
	snapshots := decodeStepSnapshots(t, releases[0].StepsJson)
	assertSnapshot(t, snapshots, expectedSnapshot{0, "first", 1})
	assertSnapshot(t, snapshots, expectedSnapshot{1, "second", 2})
	assertSnapshot(t, snapshots, expectedSnapshot{2, "third", 3})

	if err := h.repo.Queries.DeleteStep(ctx, second.ID); err != nil {
		t.Fatalf("delete step: %v", err)
	}
	refreshForm := url.Values{}
	refreshForm.Set("csrf_token", h.csrfToken())

	// When
	resp, err = h.authedClient().PostForm(
		fmt.Sprintf(
			"%s/projects/%d/releases/%d/refresh",
			h.server.URL,
			proj.ID,
			releases[0].ID,
		),
		refreshForm,
	)
	if err != nil {
		t.Fatalf("POST release refresh: %v", err)
	}
	resp.Body.Close()

	// Then
	refreshed, err := h.repo.Queries.GetRelease(ctx, releases[0].ID)
	if err != nil {
		t.Fatalf("get refreshed release: %v", err)
	}
	snapshots = decodeStepSnapshots(t, refreshed.StepsJson)
	assertSnapshot(t, snapshots, expectedSnapshot{0, "first", 1})
	assertSnapshot(t, snapshots, expectedSnapshot{1, "third", 2})

	storedFirst, err := h.repo.Queries.GetStep(ctx, first.ID)
	if err != nil {
		t.Fatalf("get first step: %v", err)
	}
	storedThird, err := h.repo.Queries.GetStep(ctx, third.ID)
	if err != nil {
		t.Fatalf("get third step: %v", err)
	}
	if storedFirst.SortOrder != 10 || storedThird.SortOrder != 30 {
		t.Fatalf(
			"stored sort orders = [%d, %d]",
			storedFirst.SortOrder,
			storedThird.SortOrder,
		)
	}
}

type stepSnapshot struct {
	Name      string `json:"name"`
	SortOrder int64  `json:"sort_order"`
}

type expectedSnapshot struct {
	Index     int
	Name      string
	SortOrder int64
}

func decodeStepSnapshots(t *testing.T, raw string) []stepSnapshot {
	t.Helper()
	var snapshots []stepSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshots); err != nil {
		t.Fatalf("decode release steps: %v", err)
	}
	return snapshots
}

func assertSnapshot(
	t *testing.T,
	snapshots []stepSnapshot,
	want expectedSnapshot,
) {
	t.Helper()
	if len(snapshots) <= want.Index {
		t.Fatalf(
			"expected snapshot index %d, got %d steps",
			want.Index,
			len(snapshots),
		)
	}
	got := snapshots[want.Index]
	if got.Name != want.Name || got.SortOrder != want.SortOrder {
		t.Fatalf(
			"snapshot[%d] = (%q, %d)",
			want.Index,
			got.Name,
			got.SortOrder,
		)
	}
}
