//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"slices"
	"testing"

	"durpdeploy/internal/db"
)

func TestLifecycleStageOrderBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	var lifecycle struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Stage order browser"}, 201), &lifecycle)
	api := fmt.Sprintf("/api/v1/lifecycles/%d", lifecycle.ID)
	f.api(t, "POST", api+"/stages", map[string]any{
		"environment_id":    f.environment.ID,
		"sort_order":        10,
		"requires_approval": true,
	}, 201)
	var next, stale struct{ ID int64 }
	for name, target := range map[string]*struct{ ID int64 }{
		"Browser append": &next, "Stale append": &stale,
	} {
		decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/environments",
			map[string]string{"name": name}, 201), target)
	}
	stages := func() []db.LifecycleStage {
		t.Helper()
		var saved struct{ Stages []db.LifecycleStage }
		decodeLifecycleTest(t, f.api(t, "GET", api, nil, 200), &saved)
		return saved.Stages
	}
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	path := fmt.Sprintf("/lifecycles/%d", lifecycle.ID)
	b.navigateBackTest(t, f.baseURL+path)
	submit := func(id int64) {
		t.Helper()
		b.evaluate(t, fmt.Sprintf(
			`(() => { const form=document.querySelector('[data-lifecycle-environment-assignment]'); form.querySelector('select').value=%q; form.requestSubmit(); return true; })()`,
			fmt.Sprint(id),
		))
	}
	submit(next.ID)
	b.wait(
		t,
		fmt.Sprintf(
			`!document.querySelector('[data-lifecycle-environment-assignment] select option[value="%d"]') && !document.querySelector('.htmx-request,.htmx-settling')`,
			next.ID,
		),
	)
	saved := stages()
	if len(saved) != 2 || saved[0].SortOrder != 10 ||
		saved[0].RequiresApproval != 1 || saved[1].SortOrder != 11 {
		t.Fatalf("web append changed explicit order or approval: %+v", saved)
	}
	b.navigateBackTest(t, f.baseURL+path)
	// Another client fills the stale form's selected environment first.
	f.api(t, "POST", api+"/stages",
		map[string]int64{"environment_id": stale.ID}, 201)
	before := stages()
	b.evaluate(
		t,
		`window.stageConflictStatus=0;document.body.addEventListener('htmx:responseError',e=>{window.stageConflictStatus=e.detail.xhr.status});true`,
	)
	submit(stale.ID)
	b.wait(
		t,
		`window.stageConflictStatus===400 && !document.querySelector('.htmx-request')`,
	)
	if !slices.Equal(before, stages()) {
		t.Fatal("stale web form changed stage data")
	}
	if string(b.evaluate(t, fmt.Sprintf(
		`location.pathname===%q && !!document.querySelector('[data-lifecycle-environment-assignment]')`,
		path,
	))) != "true" {
		t.Fatal("web conflict discarded the lifecycle workspace")
	}
	b.navigateBackTest(t, f.baseURL+path)
	before = stages()
	for _, direction := range []string{"down", "up"} {
		b.evaluate(t, fmt.Sprintf(`(() => {
const form = [...document.querySelectorAll('form[action$="/stages/reorder"]')]
 .find(f => f.querySelector('[name=stage_id]').value === %q &&
 f.querySelector('[name=direction]').value === %q);
form.querySelector('button').click(); return true;
})()`, fmt.Sprint(before[0].ID), direction))
		firstID := before[1].ID
		if direction == "up" {
			firstID = before[0].ID
		}
		b.wait(t, fmt.Sprintf(
			`document.querySelector('form[action$="/stages/reorder"] [name=stage_id]').value === %q && !document.querySelector('.htmx-request,.htmx-settling')`,
			fmt.Sprint(firstID),
		))
		want := slices.Clone(before)
		if direction == "down" {
			want[0], want[1] = want[1], want[0]
			want[0].SortOrder, want[1].SortOrder = before[0].SortOrder, before[1].SortOrder
		}
		if !slices.Equal(want, stages()) {
			t.Fatal("rendered move control changed stage metadata")
		}
		b.captureNavigation(t, "lifecycle-stage-order-"+direction)
	}
	b.captureNavigation(t, "lifecycle-stage-order-recovery")
}
