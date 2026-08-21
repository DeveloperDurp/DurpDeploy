package pages

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func renderAgentAdminPage(
	t *testing.T,
	ctx context.Context,
	component templ.Component,
) string {
	t.Helper()
	var output bytes.Buffer
	if err := component.Render(ctx, &output); err != nil {
		t.Fatalf("render agent admin page: %v", err)
	}
	return output.String()
}

func TestAgentsPage_renders_admin_navigation_and_fixed_table(t *testing.T) {
	// Given
	request := auth.SetUser(
		httptest.NewRequest("GET", "/admin/agents", nil),
		&db.User{Role: "admin"},
	)
	view := AgentsView{
		Agents: []db.Agent{
			{ID: "agent-one", Name: "Agent One", Status: "pending"},
		},
		CurrentPath: "/admin/agents",
	}

	// When
	markup := renderAgentAdminPage(t, request.Context(), AgentsPage(view))

	// Then
	if count := strings.Count(
		markup,
		`href="/admin/agents" class="active"`,
	); count != 2 {
		t.Fatalf(
			"active Admin > Agents links = %d, want 2 for mobile and desktop",
			count,
		)
	}
	for _, required := range []string{
		`class="table table-zebra table-fixed w-full"`,
		`<caption class="sr-only">Remote agents</caption>`,
		`class="btn btn-primary btn-sm">New agent</a>`,
	} {
		if !strings.Contains(markup, required) {
			t.Errorf("agents markup is missing %q", required)
		}
	}
}
