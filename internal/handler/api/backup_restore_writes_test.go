//go:build e2e && backuprestore && linux

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

const restorePublicValue = "retained-public-value 雪 café 🚀"

func (f *restoreFixture) assertNewWrites(t *testing.T, watermark int64) {
	t.Helper()
	var project, saved struct {
		ID   int64
		Name string
	}
	decodeStepLogTest(t, f.api.api(t, "POST", "/api/v1/projects",
		map[string]string{"name": "Recovered writes 雪 café 🚀"}, 201), &project)
	if project.ID <= watermark {
		t.Fatal("restored project sequence or identity regressed")
	}
	decodeStepLogTest(
		t,
		f.api.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/projects/%d", project.ID),
			nil,
			200,
		),
		&saved,
	)
	if saved.Name != project.Name {
		t.Fatal("restored application changed Unicode data")
	}
	var environment struct{ ID int64 }
	decodeStepLogTest(t, f.api.api(
		t,
		"POST",
		"/api/v1/environments",
		map[string]string{
			"name": "Recovered environment 雪 café 🚀",
		},
		201,
	), &environment)
	if environment.ID <= f.api.environment.ID {
		t.Fatal("restored environment sequence or identity regressed")
	}
	var release db.Release
	decodeStepLogTest(t, f.api.api(t, "POST", f.api.base()+"/releases",
		map[string]string{"version": "recovery-v2"}, 201), &release)
	if release.ID <= f.releaseID {
		t.Fatal("restored release sequence or identity regressed")
	}
	page := f.api.web(
		t,
		"GET",
		fmt.Sprintf("/projects/%d", project.ID),
		nil,
		200,
	)
	if !strings.Contains(page, project.Name) {
		t.Fatal("new Unicode data is unavailable through the web")
	}
}
