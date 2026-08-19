package pages

import (
	"bytes"
	"context"
	"database/sql"
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

func TestAgentDetailsPage_renders_accessible_redacted_metadata(t *testing.T) {
	// Given
	view := AgentDetailView{
		Agent: db.Agent{
			ID:     "agent-one",
			Name:   "Agent One",
			Status: "active",
			CertificatePem: sql.NullString{
				String: "PRIVATE PEM MUST NOT RENDER",
				Valid:  true,
			},
			CertificateFingerprint: sql.NullString{
				String: "sha256:0123456789abcdef",
				Valid:  true,
			},
		},
		Tags: []db.ListAgentTagsByAgentRow{{
			TagKey:   "region",
			TagValue: "west",
		}},
		Events: []db.ListRedactedAgentEventsByAgentRow{{
			EventType: "enrolled",
		}},
		CurrentPath: "/admin/agents/agent-one",
	}

	// When
	markup := renderAgentAdminPage(
		t,
		context.Background(),
		AgentDetailsPage(view),
	)

	// Then
	for _, required := range []string{
		`class="table table-zebra table-fixed w-full"`,
		`<label class="label" for="agent-tag-key">`,
		`id="agent-tag-key"`,
		`aria-label="Copy certificate fingerprint"`,
		`data-copy-value="sha256:0123456789abcdef"`,
		`sha256:01234567...`,
		`<caption class="sr-only">Agent events</caption>`,
	} {
		if !strings.Contains(markup, required) {
			t.Errorf("agent detail markup is missing %q", required)
		}
	}
	if strings.Contains(markup, "PRIVATE PEM MUST NOT RENDER") {
		t.Fatal("agent detail rendered the certificate PEM")
	}
}

func TestAgentEnrollmentPage_hides_one_time_token_from_viewer(t *testing.T) {
	// Given
	request := auth.SetUser(
		httptest.NewRequest("GET", "/admin/agents/agent-one/enrollment", nil),
		&db.User{Role: "viewer"},
	)
	view := AgentEnrollmentView{
		Agent:       db.Agent{ID: "agent-one", Name: "Agent One"},
		Token:       "ddp_enroll_secret",
		CurrentPath: "/admin/agents/agent-one/enrollment",
	}

	// When
	markup := renderAgentAdminPage(
		t,
		request.Context(),
		AgentEnrollmentPage(view),
	)

	// Then
	if !strings.Contains(markup, "Viewers cannot create an enrollment token.") {
		t.Error(
			"viewer enrollment page did not render the standard forbidden message",
		)
	}
	if strings.Contains(markup, view.Token) {
		t.Fatal("viewer enrollment page rendered the one-time enrollment token")
	}
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
