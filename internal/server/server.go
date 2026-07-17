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
) *chi.Mux {
	r := chi.NewRouter()
	r.Use(requestLogger)
	r.Use(handler.PanicRecoveryMiddleware)

	// Serve static files from embedded assets (public).
	r.Handle(
		"/static/*",
		http.StripPrefix("/static/", http.FileServer(http.FS(static.Assets))),
	)

	errorHandler := handler.NewErrorHandler()
	r.NotFound(errorHandler.NotFound)
	r.MethodNotAllowed(errorHandler.MethodNotAllowed)

	// System endpoints (public).
	healthH := handler.NewHealthHandler(repo)
	r.Get("/healthz", healthH.Healthz)

	// Auth endpoints (public).
	r.Get("/login", authHandler.LoginGet)
	r.Post("/login", authHandler.LoginPost)
	r.Post("/logout", authHandler.LogoutPost)

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

			usersH := handler.NewUsersHandler(repo)
			ar.Get("/admin/users", usersH.ListUsers)
			ar.Get("/admin/users/new", usersH.NewUserForm)
			ar.Post("/admin/users", usersH.CreateUser)
			ar.Get("/admin/users/{id}/edit", usersH.EditUserForm)
			ar.Put("/admin/users/{id}", usersH.UpdateUser)
			ar.Delete("/admin/users/{id}", usersH.DeleteUser)
		})
	})

	return r
}
