package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/robfig/cron/v3"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/handler/api"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/static"
)

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"duration", time.Since(start).String(),
		)
	})
}

func NewRouter(
	repo *repository.Repository,
	rnr *runner.DeploymentRunner,
	parser cron.Parser,
	authHandler *handler.AuthHandler,
	oidcEnabled ...bool,
) *chi.Mux {
	registerOIDC := len(oidcEnabled) > 0 && oidcEnabled[0]
	r := chi.NewRouter()
	r.Use(requestLogger)
	r.Use(handler.PanicRecoveryMiddleware)

	// Serve static files from embedded assets (public).
	r.Handle(
		"/static/*",
		http.StripPrefix("/static/", http.FileServer(http.FS(static.Assets))),
	)
	r.Get("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	errorHandler := handler.NewErrorHandler()
	r.NotFound(errorHandler.NotFound)
	r.MethodNotAllowed(errorHandler.MethodNotAllowed)

	// System endpoints (public).
	healthH := handler.NewHealthHandler(repo)
	r.Get("/healthz", healthH.Healthz)
	r.Get("/api/v1/healthz", healthH.HealthzAPI)

	// Auth endpoints (public).
	r.Get("/login", authHandler.LoginGet)
	r.Post("/login", authHandler.LoginPost)
	r.Get("/login/mfa", authHandler.LoginMFAGet)
	r.With(authHandler.MFARateLimit).Post(
		"/login/mfa/totp", authHandler.LoginMFATOTPPost,
	)
	r.With(authHandler.MFARateLimit).Post(
		"/login/mfa/recovery", authHandler.LoginMFARecoveryPost,
	)
	r.With(authHandler.MFARateLimit).Post(
		"/login/mfa/webauthn/begin", authHandler.LoginMFAWebAuthnBegin,
	)
	r.With(authHandler.MFARateLimit).Post(
		"/login/mfa/webauthn/finish", authHandler.LoginMFAWebAuthnFinish,
	)
	r.Post("/login/mfa/cancel", authHandler.LoginMFACancelPost)
	if registerOIDC {
		r.With(authHandler.OIDCRateLimit).Get(
			"/login/oidc", authHandler.LoginOIDCGet,
		)
		r.Get("/login/oidc/callback", authHandler.LoginOIDCCallbackGet)
		r.Get("/login/oidc/failure", authHandler.LoginOIDCFailureGet)
	}

	// Protected routes: every request must carry a valid session cookie
	// and state-changing requests must carry the CSRF token.
	// ponytail: single group covers all protected routes; P1 may add
	// per-route RequireRole middleware for finer-grained authorization.
	r.Group(func(pr chi.Router) {
		pr.Use(auth.AuthMiddleware(repo))
		pr.Use(auth.CSRFMiddleware())
		pr.Use(audit.Middleware(repo))

		// Home page
		indexHandler := handler.NewIndexHandler(repo)
		pr.Get("/", indexHandler.Index)
		pr.Post("/logout", authHandler.LogoutPost)

		envHandler := handler.NewEnvironmentHandler(repo)
		pr.Get("/environments", envHandler.ListEnvironments)
		pr.Get("/environments/new", envHandler.NewEnvironment)
		pr.Post("/environments", envHandler.CreateEnvironment)
		pr.Get("/environments/{id}/edit", envHandler.EditEnvironment)
		pr.Put("/environments/{id}", envHandler.UpdateEnvironment)
		pr.Delete("/environments/{id}", envHandler.DeleteEnvironment)

		lifecycleH := handler.NewLifecycleHandler(repo)
		pr.Get("/lifecycles", lifecycleH.ListLifecycles)
		pr.Get("/lifecycles/new", lifecycleH.NewLifecycle)
		pr.Post("/lifecycles", lifecycleH.CreateLifecycle)
		pr.Get("/lifecycles/{id}", lifecycleH.GetLifecycle)
		pr.Get("/lifecycles/{id}/edit", lifecycleH.EditLifecycle)
		pr.Post("/lifecycles/{id}", lifecycleH.SaveLifecycle)
		pr.Post("/lifecycles/{id}/stages", lifecycleH.AddStage)
		pr.Post("/lifecycles/{id}/stages/reorder", lifecycleH.ReorderStage)
		pr.Patch(
			"/lifecycles/{id}/stages/{stageId}",
			lifecycleH.UpdateLifecycleStage,
		)
		pr.Post(
			"/lifecycles/{id}/stages/{stageId}/delete",
			lifecycleH.DeleteStage,
		)

		ph := handler.NewProjectHandler(repo)
		pr.Get("/projects", ph.ListProjects)
		pr.Get("/projects/new", ph.NewProject)
		pr.Post("/projects", ph.CreateProject)

		tokensH := handler.NewTokensHandler(repo)
		pr.Get("/settings/tokens", tokensH.MyTokens)
		pr.Post("/settings/tokens", tokensH.MyTokensPost)
		pr.Post("/settings/tokens/{id}/revoke", tokensH.MyTokensRevoke)

		pr.Get("/settings/security", authHandler.SecurityGet)
		pr.Get("/settings/security/reauth", authHandler.SecurityReauthGet)
		pr.Post("/settings/security/reauth", authHandler.SecurityReauthPost)
		if registerOIDC {
			pr.Get(
				"/settings/security/reauth/oidc",
				authHandler.SecurityReauthOIDCGet,
			)
		}
		pr.Post(
			"/settings/security/reauth/totp",
			authHandler.SecurityReauthTOTPPost,
		)
		pr.Post(
			"/settings/security/reauth/recovery",
			authHandler.SecurityReauthRecoveryPost,
		)
		pr.Post(
			"/settings/security/recovery/continue",
			authHandler.SecurityRecoveryContinuePost,
		)
		pr.Post(
			"/settings/security/reauth/webauthn/begin",
			authHandler.SecurityReauthWebAuthnBegin,
		)
		pr.Post(
			"/settings/security/reauth/webauthn/finish",
			authHandler.SecurityReauthWebAuthnFinish,
		)
		pr.Post(
			"/settings/security/totp/begin",
			authHandler.SecurityTOTPBeginPost,
		)
		pr.Post(
			"/settings/security/totp/confirm",
			authHandler.SecurityTOTPConfirmPost,
		)
		pr.Post(
			"/settings/security/passkeys/begin",
			authHandler.SecurityPasskeyBeginPost,
		)
		pr.Post(
			"/settings/security/passkeys/finish",
			authHandler.SecurityPasskeyFinishPost,
		)
		pr.Get(
			"/settings/security/passkeys/test",
			authHandler.SecurityPasskeyTestGet,
		)
		pr.Post(
			"/settings/security/passkeys/test/begin",
			authHandler.SecurityPasskeyTestBeginPost,
		)
		pr.Post(
			"/settings/security/passkeys/test/finish",
			authHandler.SecurityPasskeyTestFinishPost,
		)
		pr.Post(
			"/settings/security/passkeys/test/cancel",
			authHandler.SecurityPasskeyTestCancelPost,
		)
		pr.Post(
			"/settings/security/passkeys/rename",
			authHandler.SecurityPasskeyRenamePost,
		)
		pr.Post(
			"/settings/security/passkeys/delete",
			authHandler.SecurityPasskeyDeletePost,
		)
		pr.Post(
			"/settings/security/recovery/regenerate",
			authHandler.SecurityRecoveryRegeneratePost,
		)
		pr.Post("/settings/security/disable", authHandler.SecurityDisablePost)

		sh := handler.NewStepHandler(repo)

		sth := handler.NewStepTemplateHandler(repo)
		pr.Get("/templates", sth.ListTemplates)
		pr.Get("/templates/new", sth.NewTemplateForm)
		pr.Post("/templates", sth.CreateTemplate)
		pr.Get("/templates/{id}/edit", sth.EditTemplateForm)
		pr.Put("/templates/{id}", sth.UpdateTemplate)
		pr.Delete("/templates/{id}", sth.DeleteTemplate)
		pr.Get("/templates/{id}/history", sth.ListTemplateHistory)

		vh := handler.NewVariableHandler(repo)

		rh := handler.NewReleaseHandler(repo)

		dh := handler.NewDeploymentHandler(repo, rnr)
		pr.Get("/deployments", dh.ListDeployments)

		lhH := handler.NewLintHandler()
		pr.Post("/api/lint", lhH.LintScript)

		sdh := handler.NewScheduledDeploymentHandler(repo, parser)

		lh := handler.NewLogHandler(rnr.Broker(), repo)

		// Deployment detail/action/log routes: the `{id}` param is a
		// deployment id, not a project id, so membership is enforced via
		// RequireDeploymentProjectAccess (resolves deployment -> release
		// -> project) rather than RequireProjectAccess.
		pr.Group(func(dpr chi.Router) {
			dpr.Use(auth.RequireDeploymentProjectAccess(repo))

			dpr.Get("/deployments/{id}", dh.GetDeployment)
			dpr.Get("/deployments/{id}/status", dh.GetDeploymentStatus)
			dpr.Post("/deployments/{id}/cancel", dh.CancelDeployment)
			dpr.Post("/deployments/{id}/approve", dh.ApproveDeployment)
			dpr.Post("/deployments/{id}/redeploy", dh.RedeployDeployment)

			dpr.Get("/deployments/{id}/logs/stream", lh.StreamLogs)
			dpr.Get("/deployments/{id}/logs.txt", lh.ExportLogs)
		})

		// Project-scoped routes: every /projects/{id}/... request must
		// pass the per-project membership check (global admins bypass).
		// Routes that are NOT under /projects/{id}/ (project list/new,
		// templates, deployments, logs) stay on pr above.
		pr.Group(func(ppr chi.Router) {
			ppr.Use(auth.RequireProjectAccess(repo))

			ppr.Get("/projects/{id}", ph.GetProject)
			ppr.Get("/projects/{id}/edit", ph.EditProject)
			ppr.Put("/projects/{id}", ph.UpdateProject)
			ppr.Delete("/projects/{id}", ph.DeleteProject)
			ppr.Get("/projects/{id}/notifications", ph.GetProjectNotifications)
			ppr.Post(
				"/projects/{id}/notifications",
				ph.UpdateProjectNotifications,
			)

			ppr.Get("/projects/{id}/steps", sh.ListSteps)
			ppr.Get("/projects/{id}/steps-page", sh.StepsPage)
			ppr.Get("/projects/{id}/steps/new", sh.NewStepForm)
			ppr.Post("/projects/{id}/steps", sh.CreateStep)
			ppr.Get("/projects/{id}/steps/{stepId}/edit", sh.EditStepForm)
			ppr.Put("/projects/{id}/steps/{stepId}", sh.UpdateStep)
			ppr.Delete("/projects/{id}/steps/{stepId}", sh.DeleteStep)
			ppr.Patch("/projects/{id}/steps/reorder", sh.ReorderStep)

			ppr.Get("/projects/{id}/templates-picker", sth.TemplatesPicker)
			ppr.Post(
				"/projects/{id}/steps/from-template/{templateId}",
				sth.InsertTemplate,
			)
			ppr.Post(
				"/projects/{id}/steps/{stepId}/save-as-template",
				sth.SaveStepAsTemplate,
			)

			ppr.Get("/projects/{id}/variables", vh.ListVariables)
			ppr.Post("/projects/{id}/variables", vh.CreateVariable)
			ppr.Get("/projects/{id}/variables/{varId}/edit", vh.EditVariable)
			ppr.Put("/projects/{id}/variables/{varId}", vh.UpdateVariable)
			ppr.Delete("/projects/{id}/variables/{varId}", vh.DeleteVariable)

			ppr.Get("/projects/{id}/releases", rh.ListReleases)
			ppr.Post("/projects/{id}/releases", rh.CreateRelease)
			ppr.Get("/projects/{id}/releases/{releaseId}", rh.GetRelease)
			ppr.Post(
				"/projects/{id}/releases/{releaseId}/refresh",
				rh.RefreshRelease,
			)

			ppr.Get("/projects/{id}/deploy", dh.NewDeploymentPage)
			ppr.Post("/projects/{id}/deploy", dh.ScheduleDeployment)

			ppr.Get("/projects/{id}/schedules", sdh.List)
			ppr.Get("/projects/{id}/schedules/new", sdh.NewForm)
			ppr.Post("/projects/{id}/schedules", sdh.Create)
			ppr.Get("/projects/{id}/schedules/{schedId}/edit", sdh.EditForm)
			ppr.Put("/projects/{id}/schedules/{schedId}", sdh.Update)
			ppr.Delete("/projects/{id}/schedules/{schedId}", sdh.Delete)
			ppr.Post("/projects/{id}/schedules/{schedId}/toggle", sdh.Toggle)

			mh := handler.NewProjectMembersHandler(repo)
			ppr.Get("/projects/{id}/members", mh.ListMembers)
			ppr.Post("/projects/{id}/members", mh.AddMember)
			ppr.Delete("/projects/{id}/members/{userId}", mh.RemoveMember)
		})

		// Admin-only sub-group. RequireRole gates every /admin/* route so
		// non-admin roles get 403 without touching the handlers.
		pr.Group(func(ar chi.Router) {
			ar.Use(auth.RequireRole("admin"))
			adminH := handler.NewAdminHandler(repo)
			ar.Get("/admin/audit", adminH.ListAudit)
			ar.Get("/admin/notifications", adminH.ListNotifications)
			ar.Get(
				"/admin/notifications/settings",
				adminH.GetNotificationSettings,
			)
			ar.Post(
				"/admin/notifications/settings",
				adminH.UpdateNotificationSettings,
			)

			usersH := handler.NewUsersHandler(repo)
			ar.Get("/admin/users", usersH.ListUsers)
			ar.Get("/admin/users/new", usersH.NewUserForm)
			ar.Post("/admin/users", usersH.CreateUser)
			ar.Get("/admin/users/{id}/edit", usersH.EditUserForm)
			ar.Put("/admin/users/{id}", usersH.UpdateUser)
			ar.Delete("/admin/users/{id}", usersH.DeleteUser)
			ar.Post(
				"/admin/users/{id}/mfa-reset",
				authHandler.AdminMFAResetPost,
			)

			webTokensH := handler.NewTokensHandler(repo)
			ar.Get("/admin/tokens", webTokensH.AdminTokens)
			ar.Post("/admin/tokens/{id}/revoke", webTokensH.AdminTokensRevoke)
		})
	})

	// API v1 group — token-auth only, no CSRF, no session cookies.
	// Healthz is mounted above on the root mux so it stays public.
	r.Route("/api/v1", func(ar chi.Router) {
		ar.Use(auth.ApiTokenMiddleware(repo))
		ar.Use(auth.WriteBlockMiddleware())
		ar.Use(audit.Middleware(repo))

		// JSON 404/405 so unmatched /api/v1/* paths return a JSON
		// error envelope rather than the web HTML 404 page (which
		// would leak the navbar/footer to API clients).
		ar.NotFound(api.NotFoundJSON)
		ar.MethodNotAllowed(api.MethodNotAllowedJSON)

		tokensH := api.NewAPITokenHandler(repo)

		// Admin-only sub-group.
		ar.Group(func(aar chi.Router) {
			aar.Use(auth.RequireRole("admin"))
			aar.Get("/admin/tokens", tokensH.ListAllTokens)
			aar.Delete("/admin/tokens/{id}", tokensH.RevokeAnyToken)

			adminH := api.NewAdminHandler(repo)
			aar.Get("/admin/audit", adminH.AuditLog)
			aar.Get("/admin/stats", adminH.Stats)
			aar.Post("/admin/maintenance", adminH.Maintenance)
			aar.Get("/admin/db-tables", adminH.DbTables)
			aar.Get("/admin/notifications", adminH.ListNotifications)
			aar.Get(
				"/admin/notifications/settings",
				adminH.GetNotificationSettings,
			)
			aar.Put(
				"/admin/notifications/settings",
				adminH.UpdateNotificationSettings,
			)

			// /admin/users is the only path — the spec and router agree.
			// /users/me is mounted separately below so any authenticated
			// caller can see who they are.
			usersH := api.NewUserHandler(repo)
			aar.Get("/admin/users", usersH.ListUsers)
			aar.Post("/admin/users", usersH.CreateUser)
			aar.Get("/admin/users/{id}", usersH.GetUser)
			aar.Put("/admin/users/{id}", usersH.UpdateUser)
			aar.Delete("/admin/users/{id}", usersH.DeleteUser)
		})

		ar.Post("/tokens", tokensH.CreateToken)
		ar.Get("/tokens", tokensH.ListTokens)
		ar.Delete("/tokens/{id}", tokensH.RevokeToken)

		apiProjH := api.NewProjectHandler(repo)
		ar.Get("/projects", apiProjH.ListProjects)
		ar.Post("/projects", apiProjH.CreateProject)

		apiEnvH := api.NewEnvironmentHandler(repo)
		ar.Get("/environments", apiEnvH.ListEnvironments)
		ar.Post("/environments", apiEnvH.CreateEnvironment)
		ar.Get("/environments/{id}", apiEnvH.GetEnvironment)
		ar.Put("/environments/{id}", apiEnvH.UpdateEnvironment)
		ar.Delete("/environments/{id}", apiEnvH.DeleteEnvironment)

		apiLcH := api.NewLifecycleHandler(repo)
		ar.Get("/lifecycles", apiLcH.ListLifecycles)
		ar.Post("/lifecycles", apiLcH.CreateLifecycle)
		ar.Get("/lifecycles/{id}", apiLcH.GetLifecycle)
		ar.Post("/lifecycles/{id}/save", apiLcH.SaveLifecycle)
		ar.Post("/lifecycles/{id}/stages", apiLcH.AddStage)
		ar.Post("/lifecycles/{id}/stages/reorder", apiLcH.ReorderStages)
		ar.Patch("/lifecycles/{id}/stages/{stageId}", apiLcH.UpdateStage)
		ar.Post("/lifecycles/{id}/stages/{stageId}/delete", apiLcH.DeleteStage)

		apiTplH := api.NewStepTemplateHandler(repo)
		ar.Get("/templates", apiTplH.ListTemplates)
		ar.Post("/templates", apiTplH.CreateTemplate)
		ar.Get("/templates/{id}", apiTplH.GetTemplate)
		ar.Put("/templates/{id}", apiTplH.UpdateTemplate)
		ar.Delete("/templates/{id}", apiTplH.DeleteTemplate)
		ar.Get("/templates/{id}/history", apiTplH.ListTemplateHistory)

		apiRelH := api.NewReleaseHandler(repo)
		apiDepH := api.NewDeploymentHandler(repo, rnr)
		apiSchedH := api.NewScheduleHandler(repo)
		apiLogH := api.NewLogHandler(rnr.Broker(), repo)

		// Non-scoped deployment list (filtered by query params).
		ar.Get("/deployments", apiDepH.ListDeployments)

		// Deployment-scoped sub-group (deployment → release → project).
		ar.Group(func(dar chi.Router) {
			dar.Use(auth.RequireDeploymentProjectAccess(repo))

			dar.Get("/deployments/{id}", apiDepH.GetDeployment)
			dar.Get("/deployments/{id}/status", apiDepH.GetDeploymentStatus)
			dar.Get("/deployments/{id}/logs", apiDepH.ListDeploymentLogs)
			dar.Get("/deployments/{id}/logs/stream", apiLogH.StreamLogs)
			dar.Get("/deployments/{id}/logs.txt", apiLogH.ExportLogs)
			dar.Get("/deployments/{id}/logs/{logId}", apiLogH.GetLog)
			dar.Get("/deployments/{id}/events", apiDepH.DeploymentEvents)
			dar.Post("/deployments/{id}/cancel", apiDepH.CancelDeployment)
			dar.Post("/deployments/{id}/retry", apiDepH.RetryDeployment)
			dar.Post("/deployments/{id}/redeploy", apiDepH.RedeployDeployment)

			dar.Group(func(aar chi.Router) {
				aar.Use(auth.RequireRole("admin"))
				aar.Post("/deployments/{id}/approve", apiDepH.ApproveDeployment)
			})
		})

		// Project-scoped sub-group.
		ar.Group(func(par chi.Router) {
			par.Use(auth.RequireProjectAccess(repo))

			par.Get("/projects/{id}", apiProjH.GetProject)
			par.Put("/projects/{id}", apiProjH.UpdateProject)
			par.Delete("/projects/{id}", apiProjH.DeleteProject)
			par.Get(
				"/projects/{id}/notifications",
				apiProjH.GetProjectNotifications,
			)
			par.Put(
				"/projects/{id}/notifications",
				apiProjH.UpdateProjectNotifications,
			)

			apiStepH := api.NewStepHandler(repo)
			par.Get("/projects/{id}/steps", apiStepH.ListSteps)
			par.Post("/projects/{id}/steps", apiStepH.CreateStep)
			par.Get("/projects/{id}/steps/{stepId}", apiStepH.GetStep)
			par.Put("/projects/{id}/steps/{stepId}", apiStepH.UpdateStep)
			par.Delete("/projects/{id}/steps/{stepId}", apiStepH.DeleteStep)
			par.Patch("/projects/{id}/steps/reorder", apiStepH.ReorderSteps)

			apiVarH := api.NewVariableHandler(repo)
			par.Get("/projects/{id}/variables", apiVarH.ListVariables)
			par.Post("/projects/{id}/variables", apiVarH.CreateVariable)
			par.Get("/projects/{id}/variables/{varId}", apiVarH.GetVariable)
			par.Put("/projects/{id}/variables/{varId}", apiVarH.UpdateVariable)
			par.Delete(
				"/projects/{id}/variables/{varId}",
				apiVarH.DeleteVariable,
			)

			par.Get("/projects/{id}/templates-picker", apiTplH.TemplatesPicker)

			apiMemberH := api.NewMemberHandler(repo)
			par.Get("/projects/{id}/members", apiMemberH.ListMembers)
			par.Post("/projects/{id}/members", apiMemberH.AddMember)
			par.Delete(
				"/projects/{id}/members/{userId}",
				apiMemberH.RemoveMember,
			)
			par.Put(
				"/projects/{id}/members/{userId}",
				apiMemberH.UpdateMemberRole,
			)

			par.Get("/projects/{id}/releases", apiRelH.ListReleases)
			par.Post("/projects/{id}/releases", apiRelH.CreateRelease)
			par.Get("/projects/{id}/releases/{relId}", apiRelH.GetRelease)
			par.Post(
				"/projects/{id}/releases/{relId}/refresh",
				apiRelH.RefreshRelease,
			)

			par.Post("/projects/{id}/deployments", apiDepH.CreateDeployment)
			par.Get("/projects/{id}/deployments", apiDepH.ListDeployments)

			par.Get("/projects/{id}/schedules", apiSchedH.ListSchedules)
			par.Post("/projects/{id}/schedules", apiSchedH.CreateSchedule)
			par.Get("/projects/{id}/schedules/{schedId}", apiSchedH.GetSchedule)
			par.Put(
				"/projects/{id}/schedules/{schedId}",
				apiSchedH.UpdateSchedule,
			)
			par.Delete(
				"/projects/{id}/schedules/{schedId}",
				apiSchedH.DeleteSchedule,
			)
			par.Post(
				"/projects/{id}/schedules/{schedId}/toggle",
				apiSchedH.ToggleSchedule,
			)
		})

		// /users/me is the only user route that's open to every
		// authenticated caller (it returns the bearer token's own
		// user). The rest of /users/* lives on the admin sub-group
		// above so non-admins can neither read, write, nor delete
		// other users.
		apiUserH := api.NewUserHandler(repo)
		ar.Get("/users/me", apiUserH.GetCurrentUser)
	})

	swaggerH := api.NewSwaggerHandler()
	r.Get("/api/swagger/spec", swaggerH.Spec)
	r.Get("/api/swagger/index.html", swaggerH.UI)
	r.Get("/api/swagger", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/swagger/index.html", http.StatusFound)
	})
	r.Get("/api/swagger/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		swaggerH.UI(w, r)
	})

	return r
}
