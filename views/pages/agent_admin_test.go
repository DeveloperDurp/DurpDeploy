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

func TestAgentFormPage_writable_form_omits_server_generated_ID(t *testing.T) {
	// Given
	request := auth.SetUser(
		httptest.NewRequest("GET", "/admin/agents/new", nil),
		&db.User{Role: "admin"},
	)

	// When
	markup := renderAgentAdminPage(
		t,
		request.Context(),
		AgentFormPage("", "/admin/agents/new"),
	)

	// Then
	if !strings.Contains(markup, `name="name"`) {
		t.Error("writable agent form is missing the name field")
	}
	for _, forbidden := range []string{`name="id"`, "Agent ID"} {
		if strings.Contains(markup, forbidden) {
			t.Errorf("writable agent form contains %q", forbidden)
		}
	}
}

func TestAgentDetailsPage_shows_permanent_delete_for_writer(t *testing.T) {
	// Given
	request := auth.SetUser(
		httptest.NewRequest("GET", "/admin/agents/agent-one", nil),
		&db.User{Role: "admin"},
	)
	view := AgentDetailView{
		Agent: db.Agent{
			ID:     "agent-one",
			Name:   "Agent One",
			Status: "pending",
		},
		CurrentPath: "/admin/agents/agent-one",
	}

	// When
	markup := renderAgentAdminPage(t, request.Context(), AgentDetailsPage(view))

	// Then
	for _, required := range []string{
		`hx-delete="/admin/agents/agent-one"`,
		`hx-confirm="Permanently delete agent agent-one and all agent data? This cannot be undone."`,
		`class="btn btn-error"`,
		`data-toast-success="Agent deleted"`,
		`data-toast-error="Failed to delete agent"`,
		`>Permanently delete agent</button>`,
	} {
		if !strings.Contains(markup, required) {
			t.Errorf("agent markup is missing %q", required)
		}
	}
}

func TestAgentDetailsPage_shows_permanent_delete_for_agent_with_history(
	t *testing.T,
) {
	// Given
	request := auth.SetUser(
		httptest.NewRequest("GET", "/admin/agents/agent-one", nil),
		&db.User{Role: "admin"},
	)
	view := AgentDetailView{
		Agent: db.Agent{ID: "agent-one", Name: "Agent One", Status: "pending"},
		Events: []db.ListRedactedAgentEventsByAgentRow{
			{ID: 1, EventType: "pairing_confirmed"},
		},
		CurrentPath: "/admin/agents/agent-one",
	}

	// When
	markup := renderAgentAdminPage(t, request.Context(), AgentDetailsPage(view))

	// Then
	if !strings.Contains(markup, `hx-delete="/admin/agents/agent-one"`) {
		t.Error("agent with history must show permanent delete button")
	}
}

func TestAgentDetailsPage_shows_permanent_delete_for_non_pending_agents(
	t *testing.T,
) {
	for _, status := range []string{"active", "revoked"} {
		t.Run(status, func(t *testing.T) {
			// Given
			request := auth.SetUser(
				httptest.NewRequest("GET", "/admin/agents/agent-one", nil),
				&db.User{Role: "admin"},
			)
			view := AgentDetailView{
				Agent: db.Agent{
					ID:     "agent-one",
					Name:   "Agent One",
					Status: status,
				},
				CurrentPath: "/admin/agents/agent-one",
			}

			// When
			markup := renderAgentAdminPage(
				t,
				request.Context(),
				AgentDetailsPage(view),
			)

			// Then
			if !strings.Contains(
				markup,
				`hx-delete="/admin/agents/agent-one"`,
			) {
				t.Errorf("%s agent must show permanent delete button", status)
			}
		})
	}
}

func TestAgentDetailsPage_hides_delete_for_viewers(t *testing.T) {
	// Given
	request := auth.SetUser(
		httptest.NewRequest("GET", "/admin/agents/agent-one", nil),
		&db.User{Role: "viewer"},
	)
	view := AgentDetailView{
		Agent: db.Agent{
			ID:     "agent-one",
			Name:   "Agent One",
			Status: "pending",
		},
		CurrentPath: "/admin/agents/agent-one",
	}

	// When
	markup := renderAgentAdminPage(t, request.Context(), AgentDetailsPage(view))

	// Then
	if strings.Contains(markup, "hx-delete") {
		t.Error("viewer must not see a delete button")
	}
	if strings.Contains(markup, "Pair agent") {
		t.Error("viewer must not see the pairing card")
	}
}
