//go:build e2e && backuprestore && linux

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func (f *restoreFixture) assertRestored(
	t *testing.T,
	schedule db.ScheduledDeployment,
) {
	t.Helper()
	f.assertLargeEnvironmentFilter(t)
	var variables struct {
		Items []struct {
			Name, Value string
			Secret      int64
		} `json:"items"`
	}
	decodeStepLogTest(
		t,
		f.api.api(t, "GET", f.api.base()+"/variables", nil, 200),
		&variables,
	)
	if len(variables.Items) != 2 {
		t.Fatal("restore lost project variables")
	}
	for _, variable := range variables.Items {
		switch variable.Name {
		case "RESTORE_SECRET":
			if variable.Secret != 1 || variable.Value != "" {
				t.Fatal("restored secret response lost masking")
			}
		case "RESTORE_PUBLIC":
			if variable.Value != restorePublicValue {
				t.Fatal("restore changed the public variable")
			}
		default:
			t.Fatal("unexpected restored variable")
		}
	}
	var restored db.ScheduledDeployment
	decodeStepLogTest(
		t,
		f.api.api(t, "GET", f.schedulePath(schedule.ID), nil, 200),
		&restored,
	)
	if restored != schedule || restored.Enabled != 0 {
		t.Fatal("restore changed the disabled schedule")
	}
	page := f.api.web(
		t,
		"GET",
		fmt.Sprintf("/projects/%d", f.api.project.ID),
		nil,
		200,
	)
	if !strings.Contains(page, "Recovery drill") {
		t.Fatal("restored project was not available through the web")
	}
	page = f.api.web(t, "GET", "/environments", nil, 200)
	if !strings.Contains(page, "Recovery environment") {
		t.Fatal("restored environment was not available through the web")
	}
}

func (f *restoreFixture) assertLargeEnvironmentFilter(t *testing.T) {
	t.Helper()
	var response struct {
		Items []struct{ ID int64 } `json:"items"`
		Total int64                `json:"total"`
	}
	path := fmt.Sprintf(
		"%s/variables?environment_id=%d",
		f.api.base(),
		int64(1)<<31,
	)
	decodeStepLogTest(t, f.api.api(t, "GET", path, nil, 200), &response)
	if len(response.Items) != 0 || response.Total != 0 {
		t.Fatal(
			"large unassigned environment filter returned project variables",
		)
	}
}

func (f *restoreFixture) assertDelivery(t *testing.T, id int64) {
	t.Helper()
	f.smtp.await(t, id)
	f.assertDeliveryHistory(t, id)
}

func (f *restoreFixture) assertDeliveryHistory(t *testing.T, id int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var rows []db.ListNotificationEventsRow
		decodeStepLogTest(
			t,
			f.api.api(t, "GET", "/api/v1/admin/notifications", nil, 200),
			&rows,
		)
		for _, row := range rows {
			if row.DeploymentID.Int64 != id ||
				row.EventType != string(events.DeploymentSucceeded) {
				continue
			}
			var result map[string]string
			if err := json.Unmarshal([]byte(row.Results), &result); err != nil {
				t.Fatal(err)
			}
			if result["email"] != events.SMTPPlaintextStatus {
				t.Fatal(
					"SMTP delivery history did not record the local plaintext warning",
				)
			}
			page := f.api.web(t, "GET", "/admin/notifications", nil, 200)
			if !strings.Contains(
				page,
				fmt.Sprintf("Deployment #%d succeeded", id),
			) {
				t.Fatal("delivery history was not available through the web")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("SMTP success was not recorded in notification history")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *restoreFixture) schedulePath(id int64) string {
	return fmt.Sprintf("%s/schedules/%d", f.api.base(), id)
}

func (f *restoreFixture) exerciseSchedule(
	t *testing.T,
	schedule db.ScheduledDeployment,
) {
	t.Helper()
	f.api.api(t, "POST", f.schedulePath(schedule.ID)+"/toggle", nil, 200)
	deadline := time.Now().Add(90 * time.Second)
	for {
		var listed struct {
			Items []db.Deployment `json:"items"`
		}
		decodeStepLogTest(
			t,
			f.api.api(t, "GET", f.api.base()+"/deployments", nil, 200),
			&listed,
		)
		if len(listed.Items) > 3 {
			t.Fatal("restored schedule created duplicate deployments")
		}
		if len(listed.Items) == 3 {
			f.api.api(
				t,
				"POST",
				f.schedulePath(schedule.ID)+"/toggle",
				nil,
				200,
			)
			var saved db.ScheduledDeployment
			decodeStepLogTest(
				t,
				f.api.api(t, "GET", f.schedulePath(schedule.ID), nil, 200),
				&saved,
			)
			if saved.Enabled != 0 || !saved.LastFiredAt.Valid ||
				saved.NextRunAt <= saved.LastFiredAt.Int64 {
				t.Fatal("restored schedule did not advance or remain disabled")
			}
			id := restoredScheduleDeployment(t, listed.Items, schedule)
			f.assertExecution(t, id)
			f.assertDelivery(t, id)
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("restored scheduler did not create its deployment")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func restoredScheduleDeployment(
	t *testing.T,
	deployments []db.Deployment,
	schedule db.ScheduledDeployment,
) int64 {
	t.Helper()
	var id int64
	note := fmt.Sprintf("Scheduled: %d - %s", schedule.ID, schedule.Note.String)
	for _, deployment := range deployments {
		if deployment.Note.String == note {
			id = deployment.ID
		}
	}
	if id == 0 {
		t.Fatal("restored scheduler deployment was not present")
	}
	return id
}
