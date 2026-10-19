//go:build e2e

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/scheduler"
)

func TestScheduledOccurrenceSingleDeploymentAPIWebE2E(t *testing.T) {
	// Given: a public API schedule and two schedulers sharing its database.
	f := newArtifactE2E(t)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "scheduled-step", "script_body": "echo schedule",
		"interpreter": "bash", "execution_target": "local",
		"container_image": "harbor.durp.info/dockerhub/library/alpine:3.23",
	}, 201)
	var release db.Release
	if err := json.Unmarshal(f.api(t, "POST", f.base()+"/releases",
		map[string]string{"version": "atomic-schedule"}, 201), &release); err != nil {
		t.Fatal(err)
	}
	var schedule db.ScheduledDeployment
	if err := json.Unmarshal(f.api(t, "POST", f.base()+"/schedules",
		map[string]any{
			"release_id": release.ID, "environment_id": f.environment.ID,
			"cron": "* * * * *", "enabled": true,
		}, 201), &schedule); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(schedule.NextRunAt+1, 0)
	var schedulers []*scheduler.Scheduler
	for range 2 {
		s := scheduler.New(f.h.repo, f.h.runner,
			scheduler.WithNow(func() time.Time { return now }))
		// Isolate occurrence creation from script execution, as scheduler fixtures do.
		s.SetRunFunc(func(context.Context, int64, int64, int64) {})
		schedulers = append(schedulers, s)
	}
	// When: competing schedulers evaluate the same due instant.
	var workers sync.WaitGroup
	for _, s := range schedulers {
		workers.Go(func() { s.Tick(t.Context()) })
	}
	workers.Wait()
	// Then: the public API exposes one deployment and one advanced cursor.
	var listed struct {
		Items []db.Deployment `json:"items"`
		Total int             `json:"total"`
	}
	if err := json.Unmarshal(f.api(t, "GET", f.base()+"/deployments", nil, 200),
		&listed); err != nil {
		t.Fatal(err)
	}
	if listed.Total != 1 || len(listed.Items) != 1 {
		t.Fatalf(
			"scheduled deployment total/items=%d/%d",
			listed.Total,
			len(listed.Items),
		)
	}
	var saved db.ScheduledDeployment
	path := fmt.Sprintf("%s/schedules/%d", f.base(), schedule.ID)
	if err := json.Unmarshal(f.api(t, "GET", path, nil, 200), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.NextRunAt <= now.Unix() || !saved.LastFiredAt.Valid ||
		saved.LastFiredAt.Int64 != now.Unix() {
		t.Fatal("schedule cursor and fired time were not committed")
	}
	page := f.web(
		t,
		"GET",
		fmt.Sprintf("/projects/%d/schedules", f.project.ID),
		nil,
		200,
	)
	if !strings.Contains(
		page,
		fmt.Sprintf("/projects/%d/schedules/%d/edit", f.project.ID, saved.ID),
	) {
		t.Fatal("scheduled occurrence is missing from the web schedule list")
	}
	f.api(t, "POST", path+"/toggle", nil, 200)
}
