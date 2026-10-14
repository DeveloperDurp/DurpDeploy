package audit

import (
	"database/sql"
	"net/http"

	"durpdeploy/internal/db"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type requestStateKey struct{}

type requestState struct {
	deletedRunbook     db.Runbook
	suppress           bool
	overridden         *actionOverride
	agentLabel         string
	agentEnvironmentID int64
	agentActor         agentproto.AgentID
}

type requestOutcome struct {
	status int
	action string
	reason string
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
