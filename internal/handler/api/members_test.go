package api_test

import (
	"context"
	"net/http"
	"testing"

	"durpdeploy/internal/db"
)

func seedProjectMember(
	t *testing.T,
	h *testHarness,
	projectID, userID int64,
	role string,
) {
	t.Helper()
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: projectID,
			UserID:    userID,
			Role:      role,
		},
	); err != nil {
		t.Fatalf("add project member: %v", err)
	}
}

func TestMember_ListMembers(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)

	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/projects/"+itoa(p.ID)+"/members",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)

	list := decodeList(t, rec)
	if len(list) != 1 {
		t.Fatalf("expected 1 member, got %d", len(list))
	}
	if list[0]["user_id"] != float64(admin.ID) {
		t.Fatalf("expected user_id %d, got %v", admin.ID, list[0]["user_id"])
	}
}

func TestMember_ListMembers_ProjectNotFound(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)

	rec := h.request(
		t,
		http.MethodGet,
		"/api/v1/projects/999/members",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusNotFound)
}

func TestMember_AddMember_Success(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)
	deployer := h.seedUser(t, "deployer@example.com", "deployer")

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects/"+itoa(p.ID)+"/members",
		token,
		`{"user_id":`+itoa(deployer.ID)+`,"role":"deployer"}`,
	)
	h.assertStatus(t, rec, http.StatusCreated)
	h.assertJSONField(t, rec, "user_id", float64(deployer.ID))
	h.assertJSONField(t, rec, "role", "deployer")
}

func TestMember_AddMember_ProjectAdmin(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	p := h.seedProject(t, admin)
	projAdmin := h.seedUser(t, "projadmin@example.com", "deployer")
	seedProjectMember(t, h, p.ID, projAdmin.ID, "admin")
	projAdminToken := h.seedToken(t, projAdmin)
	deployer := h.seedUser(t, "deployer@example.com", "deployer")

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects/"+itoa(p.ID)+"/members",
		projAdminToken,
		`{"user_id":`+itoa(deployer.ID)+`,"role":"deployer"}`,
	)
	h.assertStatus(t, rec, http.StatusCreated)
}

func TestMember_AddMember_ViewerRoleRejected(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)
	viewer := h.seedUser(t, "viewer@example.com", "viewer")

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects/"+itoa(p.ID)+"/members",
		token,
		`{"user_id":`+itoa(viewer.ID)+`,"role":"viewer"}`,
	)
	h.assertStatus(t, rec, http.StatusUnprocessableEntity)
}

func TestMember_AddMember_DeployerForbidden(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	p := h.seedProject(t, admin)
	deployer := h.seedUser(t, "deployer@example.com", "deployer")
	seedProjectMember(t, h, p.ID, deployer.ID, "deployer")
	deployerToken := h.seedToken(t, deployer)
	viewer := h.seedUser(t, "viewer@example.com", "viewer")

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects/"+itoa(p.ID)+"/members",
		deployerToken,
		`{"user_id":`+itoa(viewer.ID)+`,"role":"deployer"}`,
	)
	h.assertStatus(t, rec, http.StatusForbidden)
}

func TestMember_AddMember_UserNotFound(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects/"+itoa(p.ID)+"/members",
		token,
		`{"user_id":999,"role":"deployer"}`,
	)
	h.assertStatus(t, rec, http.StatusBadRequest)
}

func TestMember_AddMember_ProjectNotFound(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	deployer := h.seedUser(t, "deployer@example.com", "deployer")

	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects/999/members",
		token,
		`{"user_id":`+itoa(deployer.ID)+`,"role":"deployer"}`,
	)
	h.assertStatus(t, rec, http.StatusNotFound)
}

func TestMember_RemoveMember_Success(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)
	deployer := h.seedUser(t, "deployer@example.com", "deployer")
	seedProjectMember(t, h, p.ID, deployer.ID, "deployer")

	rec := h.request(
		t,
		http.MethodDelete,
		"/api/v1/projects/"+itoa(p.ID)+"/members/"+itoa(deployer.ID),
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusNoContent)

	rec = h.request(
		t,
		http.MethodGet,
		"/api/v1/projects/"+itoa(p.ID)+"/members",
		token,
		"",
	)
	h.assertStatus(t, rec, http.StatusOK)
	list := decodeList(t, rec)
	if len(list) != 1 {
		t.Fatalf("expected 1 member after removal, got %d", len(list))
	}
}

func TestMember_RemoveMember_DeployerForbidden(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	p := h.seedProject(t, admin)
	deployer := h.seedUser(t, "deployer@example.com", "deployer")
	seedProjectMember(t, h, p.ID, deployer.ID, "deployer")
	deployerToken := h.seedToken(t, deployer)

	rec := h.request(
		t,
		http.MethodDelete,
		"/api/v1/projects/"+itoa(p.ID)+"/members/"+itoa(admin.ID),
		deployerToken,
		"",
	)
	h.assertStatus(t, rec, http.StatusForbidden)
}
