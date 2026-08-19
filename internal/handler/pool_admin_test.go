package handler_test

import (
	"context"
	"net/http"
	"testing"
)

func TestPoolAdmin_validates_conflicts_and_audits_membership(t *testing.T) {
	// Given
	h := newHarness(t)
	invalid := adminRequest(
		t,
		h,
		h.sess,
		http.MethodPost,
		"/admin/pools",
		`{"name":""}`,
		true,
	)
	pool := adminRequest(
		t,
		h,
		h.sess,
		http.MethodPost,
		"/admin/pools",
		`{"name":"build-fleet","description":"Build fleet"}`,
		true,
	)
	agent := adminRequest(
		t,
		h,
		h.sess,
		http.MethodPost,
		"/admin/agents",
		`{"id":"pool-agent","name":"Pool Agent"}`,
		true,
	)
	requireAdminStatus(t, invalid, http.StatusUnprocessableEntity)
	requireAdminStatus(t, pool, http.StatusCreated)
	requireAdminStatus(t, agent, http.StatusCreated)

	// When
	duplicate := adminRequest(
		t,
		h,
		h.sess,
		http.MethodPost,
		"/admin/pools",
		`{"name":"build-fleet"}`,
		true,
	)
	add := adminRequest(
		t,
		h,
		h.sess,
		http.MethodPost,
		"/admin/pools/1/members",
		`{"agent_id":"pool-agent"}`,
		true,
	)
	conflict := adminRequest(
		t,
		h,
		h.sess,
		http.MethodPost,
		"/admin/pools/1/members",
		`{"agent_id":"pool-agent"}`,
		true,
	)
	malformedTag := adminRequest(
		t,
		h,
		h.sess,
		http.MethodPut,
		"/admin/agents/pool-agent/tags/invalid%20key",
		`{"value":"value"}`,
		true,
	)

	// Then
	requireAdminStatus(t, duplicate, http.StatusConflict)
	requireAdminStatus(t, add, http.StatusNoContent)
	requireAdminStatus(t, conflict, http.StatusConflict)
	requireAdminStatus(t, malformedTag, http.StatusUnprocessableEntity)
	members, err := h.repo.Queries.ListAgentPoolMembers(context.Background(), 1)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 1 || members[0].ID != "pool-agent" {
		t.Fatalf("pool members = %#v", members)
	}
	if !hasAuditAction(t, h, "create_agent_pool") ||
		!hasAuditAction(t, h, "add_agent_pool_member") {
		t.Fatal("pool audit actions are missing")
	}
}
