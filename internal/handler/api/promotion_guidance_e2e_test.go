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

type promotionGuidanceFixture struct {
	f            *artifactE2E
	operator     artifactE2E
	release      db.Release
	environments []db.Environment
}

func newPromotionGuidance(t *testing.T) promotionGuidanceFixture {
	t.Helper()
	f := newArtifactE2E(t)
	var envs []db.Environment
	for _, name := range []string{"Dev <&>", strings.Repeat("TestEnvironment", 4), "Prod"} {
		var env db.Environment
		decodeStepLogTest(
			t,
			f.api(
				t,
				"POST",
				"/api/v1/environments",
				map[string]string{"name": name},
				201,
			),
			&env,
		)
		envs = append(envs, env)
	}
	var lifecycle struct{ ID int64 }
	decodeStepLogTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/lifecycles",
			map[string]string{"name": "Promotion path"},
			201,
		),
		&lifecycle,
	)
	for index, env := range envs {
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID),
			map[string]int64{
				"environment_id": env.ID,
				"sort_order":     int64(index + 1),
			},
			201,
		)
	}
	f.api(
		t,
		"PUT",
		f.base(),
		map[string]any{"name": f.project.Name, "lifecycle_id": lifecycle.ID},
		200,
	)
	f.api(
		t,
		"POST",
		f.base()+"/steps",
		map[string]string{
			"name":            "Promote",
			"script_body":     "echo promotion-ok",
			"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		},
		201,
	)
	release := verificationRelease(t, f, "promotion-v1")
	operator, id := newDeletionMember(t, f, "deployer")
	f.api(
		t,
		"POST",
		f.base()+"/members",
		map[string]any{"user_id": id, "role": "deployer"},
		201,
	)
	return promotionGuidanceFixture{
		f:            f,
		operator:     operator,
		release:      release,
		environments: envs,
	}
}

func (fixture promotionGuidanceFixture) deploy(t *testing.T, index int) {
	t.Helper()
	var deployment db.Deployment
	decodeStepLogTest(
		t,
		fixture.operator.api(
			t,
			"POST",
			fixture.f.base()+"/deployments",
			map[string]int64{
				"release_id":     fixture.release.ID,
				"environment_id": fixture.environments[index].ID,
			},
			201,
		),
		&deployment,
	)
	fixture.f.completion(t, deployment.ID, events.DeploymentSucceeded)
}

func (fixture promotionGuidanceFixture) forceProd(t *testing.T) {
	t.Helper()
	f := fixture.f
	f.web(t, "POST", fmt.Sprintf("/projects/%d/deploy", f.project.ID),
		url.Values{
			"release_id":     {fmt.Sprint(fixture.release.ID)},
			"environment_id": {fmt.Sprint(fixture.environments[2].ID)},
			"force":          {"true"},
		}, 303)
	var page struct{ Items []db.Deployment }
	decodeStepLogTest(
		t,
		f.api(t, "GET", f.base()+"/deployments", nil, 200),
		&page,
	)
	if len(page.Items) != 1 || page.Items[0].Forced != 1 {
		t.Fatal("Administrator Force did not create the expected deployment")
	}
	f.completion(t, page.Items[0].ID, events.DeploymentSucceeded)
}

func (fixture promotionGuidanceFixture) checkPage(t *testing.T, next int) {
	t.Helper()
	f := fixture.f
	path := fmt.Sprintf(
		"/projects/%d/deploy?release_id=%d",
		f.project.ID,
		fixture.release.ID,
	)
	page := fixture.operator.web(t, "GET", path, nil, 200)
	for index, env := range fixture.environments {
		marker := fmt.Sprintf(`data-promotion-environment-id="%d"`, env.ID)
		if strings.Contains(page, marker) != (index > next) {
			t.Fatal("Guidance does not match blocked stages")
		}
		if index > next &&
			!strings.Contains(
				page,
				html.EscapeString(
					fmt.Sprintf(
						"Deploy %s successfully to %s first.",
						fixture.release.Version,
						fixture.environments[index-1].Name,
					),
				),
			) {
			t.Fatal("Guidance lost the immediate predecessor")
		}
	}
}

func TestPromotionGuidanceAPIWebE2E(t *testing.T) {
	fixture := newPromotionGuidance(t)
	fixture.forceProd(t)
	f := fixture.f
	web := fmt.Sprintf("/projects/%d/deploy", f.project.ID)
	blocked := fixture.operator.web(t, "POST", web, url.Values{
		"release_id":     {fmt.Sprint(fixture.release.ID)},
		"environment_id": {fmt.Sprint(fixture.environments[2].ID)},
	}, 422)
	if !strings.Contains(
		blocked,
		fmt.Sprintf(
			`data-promotion-environment-id="%d"`,
			fixture.environments[2].ID,
		),
	) {
		t.Fatal(
			"A forced success hid the still-required predecessor on rejection",
		)
	}
	if strings.Contains(
		fixture.operator.web(t, "GET", web, nil, 200),
		`id="promotion-guidance"`,
	) {
		t.Fatal("No-release form invents a blocked stage")
	}
	for index := range fixture.environments {
		fixture.checkPage(t, index)
		if index < 2 {
			target := fixture.environments[index+1]
			for blocked := index + 1; blocked < len(fixture.environments); blocked++ {
				var response struct{ Error string }
				decodeStepLogTest(
					t,
					fixture.operator.api(
						t,
						"POST",
						f.base()+"/deployments",
						map[string]int64{
							"release_id":     fixture.release.ID,
							"environment_id": fixture.environments[blocked].ID,
						},
						422,
					),
					&response,
				)
				if !strings.Contains(
					response.Error,
					fixture.environments[blocked-1].Name,
				) {
					t.Fatal("API gate omitted preceding environment")
				}
			}
			fixture.operator.web(
				t,
				"POST",
				web,
				url.Values{
					"release_id":     {fmt.Sprint(fixture.release.ID)},
					"environment_id": {fmt.Sprint(target.ID)},
					"force":          {"true"},
				},
				403,
			)
		}
		fixture.deploy(t, index)
	}
	var page struct{ Total int64 }
	decodeStepLogTest(
		t,
		f.api(t, "GET", f.base()+"/deployments", nil, 200),
		&page,
	)
	if page.Total != 4 {
		t.Fatal("Blocked promotion created a deployment")
	}
	other := verificationRelease(t, f, "promotion-v2")
	fixture.operator.api(
		t,
		"POST",
		f.base()+"/deployments",
		map[string]int64{
			"release_id":     other.ID,
			"environment_id": fixture.environments[2].ID,
		},
		422,
	)
}
