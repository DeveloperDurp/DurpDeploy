//go:build e2e

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"golang.org/x/net/html"
)

type projectPanelFixture struct {
	f                *artifactE2E
	other            db.Project
	empty            db.Project
	success, attempt db.Deployment
	recent           []db.Deployment
	lifecycleID      int64
}

func newProjectPanelFixture(t *testing.T) projectPanelFixture {
	t.Helper()
	f := newArtifactE2E(t)
	var step struct{ ID int64 }
	stepBody := map[string]any{
		"name": "panel script", "script_body": "echo panel-success",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/steps", stepBody, 201),
		&step,
	)
	deploy := func(version string, terminal events.Type) db.Deployment {
		t.Helper()
		var release, deployment struct{ ID int64 }
		decodeLifecycleTest(t, f.api(t, "POST", f.base()+"/releases",
			map[string]string{"version": version}, 201), &release)
		decodeLifecycleTest(t, f.api(
			t,
			"POST",
			f.base()+"/deployments",
			map[string]int64{
				"release_id":     release.ID,
				"environment_id": f.environment.ID,
			},
			201,
		), &deployment)
		f.completion(t, deployment.ID, terminal)
		row, err := f.h.repo.Queries.GetDeployment(t.Context(), deployment.ID)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	success := deploy("own-success", events.DeploymentSucceeded)
	stepBody["script_body"] = "exit 1"
	f.api(
		t,
		"PUT",
		fmt.Sprintf("%s/steps/%d", f.base(), step.ID),
		stepBody,
		200,
	)
	attempt := deploy("own-attempt", events.DeploymentFailed)
	recent := []db.Deployment{attempt}
	for range 5 {
		recent = append(recent, seedDeployment(t, f.h.repo,
			attempt.ReleaseID, f.environment.ID, "failed"))
	}
	var other struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/projects",
		map[string]string{"name": "Foreign panel project"}, 201), &other)
	var release struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(t, "POST", fmt.Sprintf("/api/v1/projects/%d/releases", other.ID),
			map[string]string{"version": "foreign-version"}, 201),
		&release,
	)
	for i := range 6 {
		row := seedDeployment(
			t,
			f.h.repo,
			release.ID,
			f.environment.ID,
			"succeeded",
		)
		if _, err := f.h.repo.DB.ExecContext(t.Context(),
			"UPDATE deployments SET created_at = ? WHERE id = ?", 300+i, row.ID,
		); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range append([]db.Deployment{success}, recent...) {
		if _, err := f.h.repo.DB.ExecContext(t.Context(),
			"UPDATE deployments SET created_at = ? WHERE id = ?", 100, row.ID,
		); err != nil {
			t.Fatal(err)
		}
	}
	var lifecycle struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Panel lifecycle"}, 201), &lifecycle)
	f.api(t, "POST", fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID),
		map[string]int64{"environment_id": f.environment.ID}, 201)
	var empty struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/projects",
		map[string]string{"name": "Untouched panel project"}, 201), &empty)
	return projectPanelFixture{f: f, other: db.Project{ID: other.ID},
		empty: db.Project{ID: empty.ID}, success: success,
		attempt: recent[len(recent)-1], recent: recent,
		lifecycleID: lifecycle.ID}
}

func TestProjectPanelScopeAPIWebE2E(t *testing.T) {
	p := newProjectPanelFixture(t)
	f := p.f
	adminToken, adminSession, adminCSRF := f.token, f.session, f.csrf
	for _, lifecycleID := range []int64{0, p.lifecycleID} {
		f.token, f.session, f.csrf = adminToken, adminSession, adminCSRF
		f.api(t, "PUT", f.base(), map[string]any{
			"name": f.project.Name, "lifecycle_id": lifecycleID,
		}, 200)
		for _, role := range []string{"admin", "viewer", "deployer"} {
			t.Run(fmt.Sprintf("%d/%s", lifecycleID, role), func(t *testing.T) {
				f.token, f.session, f.csrf = adminToken, adminSession, adminCSRF
				p.checkAPIWeb(t, lifecycleID, role)
			})
		}
	}
}

func projectPanelEnvironmentRow(
	t *testing.T,
	page, environment string,
) *html.Node {
	t.Helper()
	nodes := dashboardElements(
		dashboardDocument(t, page),
		"data-project-environment-name",
		"",
	)
	for _, node := range nodes {
		if strings.TrimSpace(dashboardText(node)) == environment {
			return node.Parent.Parent
		}
	}
	t.Fatalf("environment %q is missing from project detail", environment)
	return nil
}

func (p projectPanelFixture) checkAPIWeb(
	t *testing.T,
	lifecycleID int64,
	role string,
) {
	t.Helper()
	f := p.f
	if role != "admin" {
		user := seedAPIUser(
			t,
			f.h.repo,
			fmt.Sprintf(
				"panel-%d-%s@example.test",
				lifecycleID,
				role,
			),
			role,
		)
		if err := f.h.repo.Queries.AddProjectMember(t.Context(),
			db.AddProjectMemberParams{
				ProjectID: f.project.ID, UserID: user.ID, Role: "deployer",
			}); err != nil {
			t.Fatal(err)
		}
		_, f.token = seedAPIToken(t, f.h.repo, user.ID)
		f.session = memberRoleSession(t, f.h, user.ID)
		f.csrf = "member-role-csrf"
		if err := f.h.repo.Queries.AddProjectMember(t.Context(),
			db.AddProjectMemberParams{
				ProjectID: p.empty.ID, UserID: user.ID, Role: "deployer",
			}); err != nil {
			t.Fatal(err)
		}
	}
	p.checkDeploymentAPI(t)
	p.checkDetail(t)
	p.checkCards(t)
	home := f.web(t, "GET", "/", nil, 200)
	if !strings.Contains(home, "own-success") ||
		!strings.Contains(home, "own-attempt") {
		t.Fatal("dashboard disagrees with retained project history")
	}
	f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d", p.success.ID),
		nil,
		200,
	)
	if role != "admin" {
		f.api(
			t,
			"GET",
			fmt.Sprintf(
				"/api/v1/projects/%d/deployments",
				p.other.ID,
			),
			nil,
			403,
		)
	}
}

func (p projectPanelFixture) checkDeploymentAPI(t *testing.T) {
	t.Helper()
	f := p.f
	path := fmt.Sprintf("/projects/%d", f.project.ID)
	query := fmt.Sprintf("?env_id=%d", f.environment.ID)
	var listed struct {
		Items []db.Deployment `json:"items"`
		Total int             `json:"total"`
	}
	decodeLifecycleTest(
		t,
		f.api(t, "GET", "/api/v1"+path+"/deployments"+query,
			nil, 200),
		&listed,
	)
	if listed.Total != 7 || len(listed.Items) != 7 ||
		listed.Items[0].ID != p.attempt.ID || listed.Items[6].ID != p.success.ID {
		t.Fatalf("project API lost or mixed history: %+v", listed)
	}
	for offset, want := range []int64{p.attempt.ID, p.recent[len(p.recent)-2].ID} {
		decodeLifecycleTest(t, f.api(t, "GET",
			"/api/v1"+path+"/deployments"+query+
				fmt.Sprintf(
					"&limit=1&offset=%d",
					offset,
				), nil, 200),
			&listed)
		if listed.Total != 7 || len(listed.Items) != 1 ||
			listed.Items[0].ID != want {
			t.Fatalf(
				"project API tie pagination: %+v, want %d",
				listed,
				want,
			)
		}
	}
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"GET",
			"/api/v1"+path+"/deployments"+query+"&status=succeeded",
			nil,
			200,
		),
		&listed,
	)
	if listed.Total != 1 || len(listed.Items) != 1 ||
		listed.Items[0].ID != p.success.ID {
		t.Fatalf("project API lost its older success: %+v", listed)
	}
}

func (p projectPanelFixture) checkDetail(t *testing.T) {
	t.Helper()
	f := p.f
	path := fmt.Sprintf("/projects/%d", f.project.ID)
	row := projectPanelEnvironmentRow(
		t,
		f.web(t, "GET", path, nil, 200),
		f.environment.Name,
	)
	text := dashboardText(row)
	if !strings.Contains(text, "own-success") ||
		!strings.Contains(text, "own-attempt") ||
		strings.Contains(text, "foreign-version") ||
		strings.Contains(text, "No successful deploys") {
		t.Fatalf("project detail lost or mixed history: %s", text)
	}
	dots := dashboardElements(row, "class", "w-2")
	if len(dots) != 5 {
		t.Fatal("project streak lost or reordered its attempts")
	}
	for _, dot := range dots {
		if len(dashboardElements(dot, "title", "failed")) != 1 {
			t.Fatal("older success displaced a recent failed attempt")
		}
	}
}

func (p projectPanelFixture) checkCards(t *testing.T) {
	t.Helper()
	f := p.f
	path := fmt.Sprintf("/projects/%d", f.project.ID)
	document := dashboardDocument(
		t,
		f.web(t, "GET", "/projects", nil, 200),
	)
	links := dashboardElements(document, "href", path)
	if len(links) != 1 {
		t.Fatal("project card missing")
	}
	card := dashboardText(links[0].Parent)
	if !strings.Contains(card, "own-attempt") ||
		!strings.Contains(card, f.environment.Name) ||
		strings.Contains(card, "foreign-version") ||
		strings.Contains(card, "No environments") {
		t.Fatalf(
			"project card lost its environment history: %s",
			card,
		)
	}
	emptyLinks := dashboardElements(
		document,
		"href",
		fmt.Sprintf("/projects/%d", p.empty.ID),
	)
	if len(emptyLinks) != 1 ||
		!strings.Contains(
			dashboardText(emptyLinks[0].Parent),
			"No deployments yet",
		) {
		t.Fatal(
			"untouched project incorrectly claims no environments are available",
		)
	}
}
