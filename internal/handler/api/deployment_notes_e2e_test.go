//go:build e2e

package api_test

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

type deploymentNotesFixture struct {
	f                       *artifactE2E
	incident, rerun, noNote db.Deployment
}

func newDeploymentNotes(t *testing.T) deploymentNotesFixture {
	t.Helper()
	f := newArtifactE2E(t)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Incident", "script_body": "echo incident-test; exit 7",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}, 201)
	release := verificationRelease(t, f, "incident-v1")
	note := "incident-failure <img src=x onerror=alert(1)>\n" +
		strings.Repeat("long-incident-note-", 24) +
		"\nFull deployment details retain this line.\nfinal-note-marker"
	f.web(t, "POST", fmt.Sprintf("/projects/%d/deploy", f.project.ID),
		url.Values{
			"release_id":     {fmt.Sprint(release.ID)},
			"environment_id": {fmt.Sprint(f.environment.ID)},
			"note":           {note},
		}, 303)
	incident := latestNotesDeployment(t, f)
	f.completion(t, incident.ID, events.DeploymentFailed)
	f.web(t, "POST", fmt.Sprintf("/deployments/%d/redeploy", incident.ID),
		nil, 303)
	rerun := latestNotesDeployment(t, f)
	f.completion(t, rerun.ID, events.DeploymentFailed)
	var noNote db.Deployment
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/deployments",
		map[string]int64{
			"release_id": release.ID, "environment_id": f.environment.ID,
		}, 201), &noNote)
	f.completion(t, noNote.ID, events.DeploymentFailed)
	if incident.Note.String != note ||
		rerun.Note.String != fmt.Sprintf("Re-run of #%d", incident.ID) ||
		noNote.Note.Valid {
		t.Fatal("deployment creation did not preserve its note contracts")
	}
	return deploymentNotesFixture{f, incident, rerun, noNote}
}

func latestNotesDeployment(t *testing.T, f *artifactE2E) db.Deployment {
	t.Helper()
	var page struct{ Items []db.Deployment }
	decodeStepLogTest(t, f.api(t, "GET", f.base()+"/deployments?limit=1",
		nil, 200), &page)
	if len(page.Items) != 1 {
		t.Fatal("deployment was not created")
	}
	return page.Items[0]
}

func TestDeploymentNotesAPIWebE2E(t *testing.T) {
	fixture := newDeploymentNotes(t)
	f := fixture.f
	for _, path := range []string{
		fmt.Sprintf("/api/v1/deployments?project_id=%d", f.project.ID),
		f.base() + "/deployments",
	} {
		var page struct {
			Items []db.Deployment
			Total int64
		}
		decodeStepLogTest(t, f.api(t, "GET", path, nil, 200), &page)
		if page.Total != 3 || len(page.Items) != 3 ||
			page.Items[0].ID != fixture.noNote.ID ||
			page.Items[1].ID != fixture.rerun.ID ||
			page.Items[1].Note != fixture.rerun.Note ||
			page.Items[2].ID != fixture.incident.ID ||
			page.Items[2].Note != fixture.incident.Note {
			t.Fatal("public history lost note identities or ordering")
		}
	}
	viewer, userID := newDeletionMember(t, f, "viewer")
	before := viewer.api(t, "GET", "/api/v1/deployments", nil, 200)
	if strings.Contains(string(before), "incident-failure") {
		t.Fatal("notes escaped the project access boundary")
	}
	f.api(t, "POST", f.base()+"/members", map[string]any{
		"user_id": userID, "role": "viewer",
	}, 201)
	for _, actor := range []*artifactE2E{f, &viewer} {
		body := actor.web(t, "GET", fmt.Sprintf(
			"/deployments?project_id=%d", f.project.ID), nil, 200)
		if !strings.Contains(
			body,
			html.EscapeString(fixture.incident.Note.String),
		) ||
			!strings.Contains(body, fixture.rerun.Note.String) ||
			strings.Contains(body, "<img src=x") {
			t.Fatal("history hides saved notes or renders note markup")
		}
		checkDeploymentNoteDetail(t, actor, fixture.incident)
	}
}

func checkDeploymentNoteDetail(
	t *testing.T,
	actor *artifactE2E,
	incident db.Deployment,
) {
	t.Helper()
	detail := actor.web(t, "GET", fmt.Sprintf(
		"/deployments/%d", incident.ID), nil, 200)
	if !strings.Contains(detail, html.EscapeString(incident.Note.String)) ||
		!strings.Contains(detail, "data-deployment-note-full") ||
		strings.Contains(detail, "<img src=x") {
		t.Fatal("deployment details lost full saved note text or escaping")
	}
	var saved db.Deployment
	decodeStepLogTest(t, actor.api(t, "GET", fmt.Sprintf(
		"/api/v1/deployments/%d", incident.ID), nil, 200), &saved)
	if saved.Note != incident.Note {
		t.Fatal("note previews changed the public deployment API value")
	}
}
