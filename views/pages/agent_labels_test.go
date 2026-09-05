package pages

import (
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func TestAgentLabelPages_HideWritesForViewerAndRenderResponsiveMembers(
	t *testing.T,
) {
	// Given
	viewer := auth.SetUser(
		httptest.NewRequest("GET", "/admin/agent-labels/1", nil),
		&db.User{Role: "viewer"},
	)
	view := AgentLabelDetailView{
		Label: db.AgentLabel{ID: 1, Name: "Cat Fact"},
		Members: []db.ListAgentLabelMembershipsRow{{
			AgentID: "agent-one", AgentName: "Agent One", AgentStatus: "active",
			PairingState: sql.NullString{String: "paired", Valid: true},
		}},
		CurrentPath: "/admin/agent-labels/1",
	}

	// When
	markup := renderAgentAdminPage(
		t,
		viewer.Context(),
		AgentLabelDetailPage(view),
	)

	// Then
	for _, required := range []string{
		"Cat Fact", "Agent One", "eligible",
		`class="table table-zebra table-fixed w-full"`,
		`data-mobile-label-members`,
		`data-desktop-label-members`,
	} {
		if !strings.Contains(markup, required) {
			t.Errorf("markup missing %q", required)
		}
	}
	for _, forbidden := range []string{"Rename label", "Add member", "Delete label", "Remove"} {
		if strings.Contains(markup, forbidden) {
			t.Errorf("viewer markup contains write affordance %q", forbidden)
		}
	}
}

func TestAgentLabelDetailPage_MobileMemberActionsUseResponsiveRecords(
	t *testing.T,
) {
	// Given
	admin := auth.SetUser(
		httptest.NewRequest("GET", "/admin/agent-labels/1", nil),
		&db.User{Role: "admin"},
	)
	view := AgentLabelDetailView{
		Label: db.AgentLabel{ID: 1, Name: "Cat Fact"},
		Members: []db.ListAgentLabelMembershipsRow{{
			AgentID: "agent-one", AgentName: "Agent One", AgentStatus: "active",
			PairingState: sql.NullString{String: "paired", Valid: true},
		}},
		CurrentPath: "/admin/agent-labels/1",
	}

	// When
	markup := renderAgentAdminPage(
		t,
		admin.Context(),
		AgentLabelDetailPage(view),
	)

	// Then
	for _, required := range []string{
		`data-mobile-label-member="agent-one"`,
		`data-proof-bounds`,
		`data-proof-touch`,
		`class="btn btn-error"`,
	} {
		if !strings.Contains(markup, required) {
			t.Errorf("mobile member markup missing %q", required)
		}
	}
}
