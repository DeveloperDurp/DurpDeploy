package audit

import (
	"database/sql"
	"net/http"

	"durpdeploy/internal/db"
	"durpdeploy/internal/provenance"
	"durpdeploy/internal/repository"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type requestStateKey struct{}

type requestState struct {
	libraryTemplate    *repository.LibraryApplyResult
	releaseID          int64
	releaseSource      *provenance.Source
	projectGroupChange map[string]any
	deletedRunbook     db.Runbook
	suppress           bool
	overridden         *actionOverride
	agentLabel         string
	agentEnvironmentID int64
	agentActor         agentproto.AgentID
	tokenID            string
	tokenScope         repository.TokenScope
	previousTokenScope *repository.TokenScope
}

// SetReleaseProvenance supplies only persisted public source metadata.
func SetReleaseProvenance(r *http.Request, release db.Release) {
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok {
		source := provenance.FromRelease(release)
		state.releaseID = release.ID
		state.releaseSource = &source
	}
}

// SetProjectGroupChange supplies non-secret group metadata to the audit middleware.
func SetProjectGroupChange(
	r *http.Request,
	id int64,
	name string,
	groupID *int64,
) {
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok {
		state.projectGroupChange = map[string]any{
			"id":       id,
			"name":     name,
			"group_id": groupID,
		}
	}
}

// SetTokenChange records only non-secret token identifiers and grants.
func SetTokenChange(r *http.Request, id string, grant repository.TokenScope,
	previous *repository.TokenScope,
) {
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok {
		state.tokenID = id
		state.tokenScope = grant
		state.previousTokenScope = previous
	}
}

type requestOutcome struct {
	status int
	action string
	reason string
}

// SetSecretExportScope records the exported scope without secret values.
func SetSecretExportScope(r *http.Request, scope string, id int64) {
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok {
		state.overridden = &actionOverride{
			action:     "export_secrets",
			entityType: scope,
			entityID:   sql.NullInt64{Int64: id, Valid: true},
		}
	}
}

// SetAgentActor retains the mTLS identity for the middleware audit.
func SetAgentActor(r *http.Request, agentID agentproto.AgentID) {
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok {
		state.agentActor = agentID
	}
}

// SetAgentAssignment retains parsed JSON values for the middleware audit.
func SetAgentAssignment(r *http.Request, label string, environmentID int64) {
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok {
		state.agentLabel = label
		state.agentEnvironmentID = environmentID
	}
}

type actionOverride struct {
	action     string
	entityType string
	entityID   sql.NullInt64
	reason     string
}

// SetProjectTemplateResult lets middleware audit the newly created identity.
func SetProjectTemplateResult(
	r *http.Request,
	action, entity string,
	id int64,
) {
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok {
		state.overridden = &actionOverride{action: action, entityType: entity,
			entityID: sql.NullInt64{Int64: id, Valid: true}}
	}
}

func Suppress(r *http.Request) {
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok {
		state.suppress = true
	}
}

func SetMFAAdminReset(r *http.Request, targetID int64, reason string) {
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok {
		state.overridden = &actionOverride{
			action:     "mfa_admin_reset",
			entityType: "admin_reset",
			entityID:   sql.NullInt64{Int64: targetID, Valid: true},
			reason:     reason,
		}
	}
}
