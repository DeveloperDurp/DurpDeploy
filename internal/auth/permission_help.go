package auth

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"

	"durpdeploy/internal/roles"
)

// MembershipRoleFromContext reports membership before the global role ceiling.
// Global administrators bypass membership and have no membership in this context.
func MembershipRoleFromContext(ctx context.Context) string {
	member, _ := ctx.Value(projectRoleKey{}).(string)
	return member
}

type RoleRecovery struct {
	Message string
	Label   string
	Href    string
}

// RoleRecoveryHelp identifies each role below the action's required role.
// It describes existing checks and does not grant access.
func RoleRecoveryHelp(ctx context.Context, requiredRole string) []RoleRecovery {
	if UserFromContext(ctx) == nil {
		return nil
	}
	var help []RoleRecovery
	if !roles.AtLeast(RoleFromContext(ctx), requiredRole) {
		help = append(help, RoleRecovery{
			Message: "Ask an administrator to review your global role on the",
			Label:   "Users page", Href: "/admin/users",
		})
	}
	member := MembershipRoleFromContext(ctx)
	if id, ok := ProjectIDFromContext(ctx); ok && member != "" &&
		!roles.AtLeast(member, requiredRole) {
		help = append(help, RoleRecovery{
			Message: "Ask a project administrator to review your project role in",
			Label:   "Project member settings",
			Href:    fmt.Sprintf("/projects/%d/edit#project-members", id),
		})
	}
	return help
}

func requestRecovery(r *http.Request) []RoleRecovery {
	requiredRole := "maintainer"
	if deploymentWrite(r) {
		requiredRole = "deployer"
	}
	return RoleRecoveryHelp(r.Context(), requiredRole)
}

func permissionDeniedMessage(r *http.Request) string {
	message := unauthorizedMessage
	for _, help := range requestRecovery(r) {
		message += ". " + help.Message + " " + help.Label
	}
	return message
}

func permissionDeniedHTML(r *http.Request) string {
	var helpHTML strings.Builder
	for _, help := range requestRecovery(r) {
		helpHTML.WriteString("<p>" + html.EscapeString(help.Message) +
			` <a href="` + html.EscapeString(help.Href) + `">` +
			html.EscapeString(help.Label) + "</a>.</p>")
	}
	return strings.Replace(unauthorizedHTML, "</p>",
		"</p>"+helpHTML.String(), 1)
}
