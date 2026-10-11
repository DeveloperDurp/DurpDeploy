package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/go-chi/chi/v5"
)

func TestAgentAuditDetailsUsesTheAuthenticatedActor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		route  string
		path   string
		action string
		actor  agentproto.AgentID
		want   string
	}{
		{"browser target", "/agents/{id}", "/agents/target-agent",
			"label_agent", "", "target-agent"},
		{"mTLS publisher", "/deployments/{id}", "/deployments/42",
			"publish_staging", "authenticated-agent", "authenticated-agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given route metadata and a separately authenticated identity.
			router := chi.NewRouter()
			var got string
			router.Post(tc.route, func(w http.ResponseWriter, r *http.Request) {
				r = r.WithContext(context.WithValue(r.Context(),
					requestStateKey{}, &requestState{}))
				SetAgentActor(r, tc.actor)
				got = buildDetails(r, requestOutcome{
					status: http.StatusNoContent, action: tc.action,
				})
				w.WriteHeader(http.StatusNoContent)
			})

			// When audit details are generated for that route.
			router.ServeHTTP(httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPost, tc.path, nil))

			// Then browser targets remain intact and mTLS uses its actor.
			var details struct {
				AgentID string `json:"agent_id"`
			}
			if err := json.Unmarshal([]byte(got), &details); err != nil {
				t.Fatal(err)
			}
			if details.AgentID != tc.want {
				t.Fatalf("agent=%q want=%q", details.AgentID, tc.want)
			}
		})
	}
}
