package audit_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/audit"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"
)

// These operational protocol updates use authenticated agent claims, not the
// user audit middleware. Staging publication has its own explicit audit action.
var agentProtocolOnly = map[string]string{
	"POST " + agentproto.PollPath:                      "poll pending work",
	"POST " + agentproto.StartPath:                     "acknowledge a claimed attempt",
	"POST " + agentproto.HeartbeatPath:                 "renew a claimed attempt",
	"POST " + agentproto.LogsPath:                      "stream execution logs",
	"POST " + agentproto.ResultPath:                    "report a claimed attempt result",
	"POST " + agentproto.CancelledPath:                 "acknowledge attempt cancellation",
	"POST /agent/v1/deployments/{id}/artifact":         "read the pinned package",
	"POST /agent/v1/deployments/{id}/staging/download": "read managed staging",
}

// Public authentication handlers record completed login through login_session.go.
// Pending challenge exchanges do not have an authenticated browser session yet.
var publicAuthentication = map[string]string{
	"POST /login":                     "password login records its own result",
	"POST /login/mfa/totp":            "complete a pending login factor",
	"POST /login/mfa/recovery":        "complete a pending recovery login",
	"POST /login/mfa/webauthn/begin":  "issue pending login options",
	"POST /login/mfa/webauthn/finish": "complete a pending passkey login",
	"POST /login/mfa/cancel":          "cancel a pending login",
}

// Empty action declarations must have a reason, rather than silently disabling
// audit for a new route. Handler-level Suppress still handles rejected/no-op
// redirects, logout's direct event, and unchanged assignments on named routes.
var noAuditEvent = map[string]string{
	"POST /api/lint":                                "read-only script validation",
	"POST /api/v1/admin/maintenance":                "operational maintenance command",
	"POST /login/mfa/webauthn/begin":                "pending authentication options",
	"POST /login/mfa/cancel":                        "pending authentication cancellation",
	"POST /settings/security/reauth/webauthn/begin": "reauthentication options",
	"POST /settings/security/totp/begin":            "stage a factor before confirmation",
	"POST /settings/security/passkeys/begin":        "registration options",
	"POST /settings/security/passkeys/finish":       "stage a credential before verification",
	"POST /settings/security/passkeys/test/begin":   "read staged verification options",
	"POST /settings/security/passkeys/test/cancel":  "discard staged verification",
	"POST /settings/security/recovery/continue":     "clear the recovery browser cookie",
}

func registeredRoutes(t *testing.T) (map[string]http.Handler, map[string]bool) {
	t.Helper()
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	repo := repository.New(conn)
	rnr := runner.New(repo, runner.NewLogBroker())
	web := server.NewRouter(repo, rnr,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(repo), true)
	agent := server.NewAgentRouter(&agentserver.Server{}).(*chi.Mux)
	routes, agentRoutes := map[string]http.Handler{}, map[string]bool{}
	for index, router := range []chi.Routes{web, agent} {
		if err := chi.Walk(router, func(method, pattern string, _ http.Handler,
			_ ...func(http.Handler) http.Handler,
		) error {
			key := method + " " + pattern
			if _, exists := routes[key]; exists {
				return fmt.Errorf("duplicate route %s", key)
			}
			if index == 0 {
				routes[key] = web
			} else {
				routes[key] = agent
				agentRoutes[key] = true
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return routes, agentRoutes
}

func auditCoverageErrors(
	routes map[string]http.Handler,
	actions map[string]string,
) []string {
	var problems []string
	for key := range routes {
		method, pattern, _ := strings.Cut(key, " ")
		if !slices.Contains(
			[]string{"POST", "PUT", "PATCH", "DELETE"},
			method,
		) ||
			pattern == "/static/*" {
			// The public embedded file handler does not change state.
			continue
		}
		if _, operational := agentProtocolOnly[key]; operational {
			continue
		}
		if _, declared := actions[key]; !declared {
			problems = append(problems, "missing audit declaration: "+key)
		}
	}
	for key, action := range actions {
		if _, exists := routes[key]; !exists {
			problems = append(problems, "unregistered audit declaration: "+key)
		}
		_, excluded := noAuditEvent[key]
		if (action == "") != excluded {
			problems = append(
				problems,
				"incorrect empty-action exclusion: "+key,
			)
		}
	}
	problems = append(problems, auditExclusionErrors(routes)...)
	slices.Sort(problems)
	return problems
}

func auditExclusionErrors(routes map[string]http.Handler) []string {
	var problems []string
	for _, exceptions := range []map[string]string{noAuditEvent, publicAuthentication, agentProtocolOnly} {
		for key, reason := range exceptions {
			if _, exists := routes[key]; !exists || reason == "" {
				problems = append(
					problems,
					"stale or unexplained exclusion: "+key,
				)
			}
		}
	}
	return problems
}

func TestRegisteredWritesHaveAuditAndAuthentication(t *testing.T) {
	routes, agentRoutes := registeredRoutes(t)
	if problems := auditCoverageErrors(routes, audit.RouteActionsForTest()); len(
		problems,
	) != 0 {
		t.Fatal(strings.Join(problems, "\n"))
	}
	parameter := regexp.MustCompile(`\{[^}]+\}`)
	for key, router := range routes {
		method, pattern, _ := strings.Cut(key, " ")
		if !slices.Contains(
			[]string{"POST", "PUT", "PATCH", "DELETE"},
			method,
		) ||
			pattern == "/static/*" {
			continue
		}
		if _, public := publicAuthentication[key]; public {
			continue
		}
		t.Run(key, func(t *testing.T) {
			assertAnonymousWrite(
				t,
				router,
				parameter,
				method,
				pattern,
				agentRoutes,
				key,
			)
		})
	}
}

func assertAnonymousWrite(
	t *testing.T,
	router http.Handler,
	parameter *regexp.Regexp,
	method, pattern string,
	agentRoutes map[string]bool,
	key string,
) {
	t.Helper()
	path := parameter.ReplaceAllString(pattern, "1")
	request := httptest.NewRequest(method, path, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	want := http.StatusSeeOther
	if strings.HasPrefix(pattern, "/api/v1/") || agentRoutes[key] {
		want = http.StatusUnauthorized
	}
	if response.Code != want {
		t.Fatalf(
			"anonymous write status=%d want=%d: %s",
			response.Code,
			want,
			response.Body,
		)
	}
	if want == http.StatusSeeOther &&
		response.Header().Get("Location") != "/login" {
		t.Fatalf(
			"anonymous write did not redirect to login: %v",
			response.Header(),
		)
	}
}

func TestRegisteredWriteCoverageRejectsMissingDeclaration(t *testing.T) {
	routes, _ := registeredRoutes(t)
	actions := audit.RouteActionsForTest()
	delete(actions, "PUT /api/v1/projects/{id}")
	if !slices.Contains(auditCoverageErrors(routes, actions),
		"missing audit declaration: PUT /api/v1/projects/{id}") {
		t.Fatal("coverage did not detect a missing audit declaration")
	}
}
