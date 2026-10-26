//go:build e2e

package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"durpdeploy/internal/db"
)

type panelReadCounter struct {
	db.DBTX
	reads, environments, summaries, lifecycles, stages atomic.Int64
}

func (c *panelReadCounter) count(query string) {
	c.reads.Add(1)
	for name, counter := range map[string]*atomic.Int64{
		"ListEnvironments":            &c.environments,
		"ListProjectPanelDeployments": &c.summaries,
		"GetLifecycle":                &c.lifecycles,
		"ListLifecycleStages":         &c.stages,
	} {
		if strings.Contains(query, "-- name: "+name+" :") {
			counter.Add(1)
		}
	}
}

func (c *panelReadCounter) QueryContext(
	ctx context.Context, query string, args ...any,
) (*sql.Rows, error) {
	c.count(query)
	return c.DBTX.QueryContext(ctx, query, args...)
}

func (c *panelReadCounter) QueryRowContext(
	ctx context.Context, query string, args ...any,
) *sql.Row {
	c.count(query)
	return c.DBTX.QueryRowContext(ctx, query, args...)
}

func TestProjectPanelBatchAPIWebE2E(t *testing.T) {
	f, _ := newProjectPanelFailureFixture(t)
	queries := db.New(f.h.repo.DB)
	counter := &panelReadCounter{DBTX: f.h.repo.DB}
	f.h.repo.Queries = db.New(counter)
	var lifecycle struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Shared panel lifecycle"}, 201), &lifecycle)
	projects, environments := []db.Project{
		f.project,
	}, []db.Environment{
		f.environment,
	}
	var baseline [2]int64
	for _, size := range [][2]int{{2, 2}, {12, 8}, {42, 32}} {
		for len(projects) < size[0] {
			projects = append(projects, seedProject(t, f.h.repo))
		}
		for len(environments) < size[1] {
			environments = append(environments, seedEnv(t, f.h.repo))
		}
		f.addPanelLifecycleStages(t, lifecycle.ID, environments)
		for mode, lifecycleID := range []int64{0, lifecycle.ID} {
			for _, project := range projects {
				f.api(
					t,
					"PUT",
					fmt.Sprintf("/api/v1/projects/%d", project.ID),
					map[string]any{
						"name":         project.Name,
						"lifecycle_id": lifecycleID,
					},
					200,
				)
			}
			counter.checkProjectPanelReads(
				t,
				f,
				size,
				mode,
				lifecycleID,
				&baseline,
			)
		}
	}
	// A later request must observe a changed environment. No shared cache may
	// retain the previous label on either the API or the rendered panel.
	name := "Updated environment label"
	f.api(t, "PUT", fmt.Sprintf("/api/v1/environments/%d", f.environment.ID),
		map[string]string{"name": name}, 200)
	for _, path := range []string{"/projects", fmt.Sprintf("/projects/%d", f.project.ID)} {
		if !strings.Contains(f.web(t, "GET", path, nil, 200), name) {
			t.Fatal("a later request reused stale environment data")
		}
	}
	env, err := queries.GetEnvironment(t.Context(), f.environment.ID)
	if err != nil || env.Name != name {
		t.Fatalf("environment API did not persist label: %+v %v", env, err)
	}
}

func (f *artifactE2E) addPanelLifecycleStages(
	t *testing.T, lifecycleID int64, environments []db.Environment,
) {
	t.Helper()
	for _, env := range environments {
		// Add new environments without changing the existing stage order.
		var count int
		if err := f.h.repo.DB.QueryRowContext(t.Context(),
			"SELECT COUNT(*) FROM lifecycle_stages WHERE lifecycle_id = ? AND environment_id = ?",
			lifecycleID, env.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			f.api(
				t,
				"POST",
				fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycleID),
				map[string]int64{"environment_id": env.ID},
				201,
			)
		}
	}
}

func (counter *panelReadCounter) checkProjectPanelReads(
	t *testing.T,
	f *artifactE2E,
	size [2]int,
	mode int,
	lifecycleID int64,
	baseline *[2]int64,
) {
	t.Helper()
	for _, path := range []string{"/projects", fmt.Sprintf("/projects/%d", f.project.ID)} {
		counter.reads.Store(0)
		counter.environments.Store(0)
		counter.summaries.Store(0)
		counter.lifecycles.Store(0)
		counter.stages.Store(0)
		f.web(t, "GET", path, nil, 200)
		reads := counter.reads.Load()
		t.Logf(
			"P=%d E=%d lifecycle=%d path=%s reads=%d",
			size[0],
			size[1],
			lifecycleID,
			path,
			reads,
		)
		wantLifecycle := int64(mode)
		if path != "/projects" {
			// Variable inheritance loads its lifecycle independently.
			wantLifecycle *= 2
		}
		if counter.environments.Load() != 1 ||
			counter.summaries.Load() != 1 ||
			counter.lifecycles.Load() != wantLifecycle ||
			counter.stages.Load() != int64(mode) {
			t.Fatal("panel data was not loaded once per request")
		}
		if path == "/projects" {
			if size[0] == 2 {
				baseline[mode] = reads
			} else if reads != baseline[mode] {
				t.Fatalf("reads grew with project/environment count: %d != %d", reads, baseline[mode])
			}
		}
	}
}
