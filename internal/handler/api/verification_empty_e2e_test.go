//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/scheduler"
)

func TestVerificationEmptyBashReleaseE2E(t *testing.T) {
	f := newArtifactE2E(t)
	release := verificationRelease(t, f, "empty-bash")
	configureVerification(t, f, "bash", "echo check", 5)
	body := map[string]int64{
		"release_id": release.ID, "environment_id": f.environment.ID,
	}
	f.api(t, "POST", f.base()+"/deployments", body, 422)
	f.web(t, "POST", fmt.Sprintf("/projects/%d/deploy", f.project.ID),
		url.Values{
			"release_id":     {fmt.Sprint(release.ID)},
			"environment_id": {fmt.Sprint(f.environment.ID)},
		}, 422)

	var schedule db.ScheduledDeployment
	if err := json.Unmarshal(f.api(t, "POST", f.base()+"/schedules",
		map[string]any{
			"release_id": release.ID, "environment_id": f.environment.ID,
			"cron": "* * * * *", "enabled": true,
		}, 201), &schedule); err != nil {
		t.Fatal(err)
	}
	scheduler.New(f.h.repo, f.h.runner,
		scheduler.WithNow(func() time.Time {
			return time.Now().Add(2 * time.Minute)
		})).Tick(t.Context())
	if err := json.Unmarshal(f.api(t, "GET",
		fmt.Sprintf("%s/schedules/%d", f.base(), schedule.ID), nil, 200),
		&schedule); err != nil {
		t.Fatal(err)
	}
	if schedule.Enabled != 0 ||
		!strings.Contains(schedule.LastError, "needs a deployment step") {
		t.Fatalf(
			"invalid schedule has no actionable disabled state: %+v",
			schedule,
		)
	}
	var list struct{ Items []db.Deployment }
	if err := json.Unmarshal(f.api(t, "GET", f.base()+"/deployments", nil, 200),
		&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Fatal("invalid Bash configuration created deployment rows")
	}
}
