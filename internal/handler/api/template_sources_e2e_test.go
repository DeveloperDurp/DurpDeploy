//go:build e2e

package api_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestTemplateSourcesAPIWebE2E(t *testing.T) {
	f := newProjectTemplateE2E(t)
	link := fmt.Sprintf(
		`href="/projects/%d/project-templates/new?return_to=%s"`,
		f.project.ID,
		url.QueryEscape("/templates?type=projects"),
	)
	for _, role := range []string{"maintainer", "viewer", "deployer", "admin"} {
		f.changeRole(t, "project", role)
		page := f.request(
			t,
			"GET",
			"/templates/project-source",
			"",
			"",
			f.session,
			false,
			200,
		)
		want := role == "maintainer" || role == "admin"
		if strings.Contains(string(page), link) != want {
			t.Fatalf(
				"source chooser role %s exposes incorrect source projects",
				role,
			)
		}
		f.jsonRequest(
			t,
			"GET",
			fmt.Sprintf("/api/v1/projects/%d", f.project.ID),
			nil,
			200,
		)
	}
	outsider := seedAPIUser(
		t,
		f.h.repo,
		"source-menu-outsider@example.test",
		"maintainer",
	)
	session := memberRoleSession(t, f.h, outsider.ID)
	page := f.request(
		t,
		"GET",
		"/templates/project-source",
		"",
		"",
		session,
		false,
		200,
	)
	if strings.Contains(string(page), link) {
		t.Fatal("source chooser exposed an inaccessible project")
	}
	for _, role := range []string{"viewer", "deployer"} {
		f.changeRole(t, "global", role)
		f.session = memberRoleSession(t, f.h, f.user.ID)
		f.request(
			t,
			"GET",
			"/templates/project-source",
			"",
			"",
			f.session,
			false,
			403,
		)
	}
}
