package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler/api"
)

func TestReleaseRefresh_normalizesGappedStepOrders(t *testing.T) {
	h := newAPIHarness(t)
	u := seedAPIUser(t, h.repo, "admin@example.com", "admin")
	p := seedProject(t, h.repo)
	r := seedRelease(t, h.repo, p.ID)
	ctx := context.Background()

	// Given
	if _, err := h.repo.Queries.CreateStep(ctx, db.CreateStepParams{
		ProjectID:  p.ID,
		Name:       "first",
		ScriptBody: "echo first",
		SortOrder:  10,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.repo.Queries.CreateStep(ctx, db.CreateStepParams{
		ProjectID:  p.ID,
		Name:       "second",
		ScriptBody: "echo second",
		SortOrder:  30,
	}); err != nil {
		t.Fatal(err)
	}

	// When
	req := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%d/releases/%d/refresh", p.ID, r.ID),
		nil,
	)
	req = withAPIUser(req, u)
	req = withAPIURLParam(req, "id", fmt.Sprint(p.ID))
	req = withAPIURLParam(req, "relId", fmt.Sprint(r.ID))
	rec := httptest.NewRecorder()
	api.NewReleaseHandler(h.repo).RefreshRelease(rec, req)

	// Then
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	mustDecode(t, rec.Body, &resp)
	snapshots := decodeAPIReleaseStepSnapshots(t, resp["steps_json"].(string))
	assertAPIReleaseStep(t, snapshots, expectedAPIReleaseStep{0, "first", 1})
	assertAPIReleaseStep(t, snapshots, expectedAPIReleaseStep{1, "second", 2})

	steps, err := h.repo.Queries.ListStepsByProject(ctx, p.ID)
	if err != nil {
		t.Fatalf("list stored steps: %v", err)
	}
	if steps[0].SortOrder != 10 || steps[1].SortOrder != 30 {
		t.Fatalf(
			"stored sort orders = [%d, %d]",
			steps[0].SortOrder,
			steps[1].SortOrder,
		)
	}
}

type apiReleaseStepSnapshot struct {
	Name      string `json:"name"`
	SortOrder int64  `json:"sort_order"`
}

type expectedAPIReleaseStep struct {
	Index     int
	Name      string
	SortOrder int64
}

func decodeAPIReleaseStepSnapshots(
	t *testing.T,
	raw string,
) []apiReleaseStepSnapshot {
	t.Helper()
	var snapshots []apiReleaseStepSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshots); err != nil {
		t.Fatalf("decode refreshed steps: %v", err)
	}
	return snapshots
}

func assertAPIReleaseStep(
	t *testing.T,
	snapshots []apiReleaseStepSnapshot,
	want expectedAPIReleaseStep,
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
