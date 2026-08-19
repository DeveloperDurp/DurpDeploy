package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"durpdeploy/internal/db"
)

func TestDeployAuthorization_keepsAdminDeploymentAvailableWhileLocalRunnerWrites(
	t *testing.T,
) {
	// Given: a project member can launch a local deployment.
	h := newProjectHarness(t)
	project := h.makeProject("authorization-race-project")
	environment := h.makeEnv("authorization-race-environment")
	release := h.makeRelease(project.ID, "1.0.0", "exit 0")
	member := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"authorization-race-member@test.local",
		"deployer",
	)
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: project.ID,
			UserID:    member.user.ID,
			Role:      "deployer",
		},
	); err != nil {
		t.Fatalf("add project member: %v", err)
	}
	deployURL := fmt.Sprintf("%s/projects/%d/deploy", h.server.URL, project.ID)
	memberForm := deploymentAuthorizationForm(release.ID, environment.ID)
	memberForm.Set("csrf_token", member.csrfToken)
	memberResponse, err := member.client.PostForm(deployURL, memberForm)
	if err != nil {
		t.Fatalf("member deployment: %v", err)
	}
	defer memberResponse.Body.Close()
	if memberResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"member deployment: status = %d, want 303",
			memberResponse.StatusCode,
		)
	}

	// When: an admin immediately launches the same release while the runner writes.
	adminForm := deploymentAuthorizationForm(release.ID, environment.ID)
	adminForm.Set("csrf_token", h.csrfToken())
	adminResponse, err := h.authedClient().PostForm(deployURL, adminForm)
	if err != nil {
		t.Fatalf("admin deployment: %v", err)
	}
	defer adminResponse.Body.Close()

	// Then: authorization succeeds without a transient SQLite dispatch failure.
	if adminResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"admin deployment: status = %d, want 303; body = %q",
			adminResponse.StatusCode,
			readBody(t, adminResponse),
		)
	}
}

func deploymentAuthorizationForm(releaseID, environmentID int64) url.Values {
	return url.Values{
		"release_id":     {strconv.FormatInt(releaseID, 10)},
		"environment_id": {strconv.FormatInt(environmentID, 10)},
	}
}
