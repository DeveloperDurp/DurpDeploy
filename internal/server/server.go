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
		pr.Patch("/lifecycles/{id}/stages/{stageId}", lifecycleH.UpdateLifecycleStage)
		pr.Post("/lifecycles/{id}/stages/{stageId}/delete", lifecycleH.DeleteStage)

		ph := handler.NewProjectHandler(repo)
		pr.Get("/projects", ph.ListProjects)
		pr.Get("/projects/new", ph.NewProject)
		pr.Post("/projects", ph.CreateProject)
		pr.Get("/projects/{id}", ph.GetProject)
		pr.Get("/projects/{id}/edit", ph.EditProject)
		pr.Put("/projects/{id}", ph.UpdateProject)
		pr.Delete("/projects/{id}", ph.DeleteProject)

		sh := handler.NewStepHandler(repo)
		pr.Get("/projects/{id}/steps", sh.ListSteps)
		pr.Get("/projects/{id}/steps-page", sh.StepsPage)
		pr.Get("/projects/{id}/steps/new", sh.NewStepForm)
		pr.Post("/projects/{id}/steps", sh.CreateStep)
		pr.Get("/projects/{id}/steps/{stepId}/edit", sh.EditStepForm)
		pr.Put("/projects/{id}/steps/{stepId}", sh.UpdateStep)
		pr.Delete("/projects/{id}/steps/{stepId}", sh.DeleteStep)
		pr.Patch("/projects/{id}/steps/reorder", sh.ReorderStep)

		sth := handler.NewStepTemplateHandler(repo)
		pr.Get("/templates", sth.ListTemplates)
		pr.Get("/templates/new", sth.NewTemplateForm)
		pr.Post("/templates", sth.CreateTemplate)
		pr.Get("/templates/{id}/edit", sth.EditTemplateForm)
		pr.Put("/templates/{id}", sth.UpdateTemplate)
		pr.Delete("/templates/{id}", sth.DeleteTemplate)
		pr.Get("/templates/{id}/history", sth.ListTemplateHistory)
		pr.Get("/projects/{id}/templates-picker", sth.TemplatesPicker)
		pr.Post(
			"/projects/{id}/steps/from-template/{templateId}",
			sth.InsertTemplate,
		)
		pr.Post(
			"/projects/{id}/steps/{stepId}/save-as-template",
			sth.SaveStepAsTemplate,
		)

		vh := handler.NewVariableHandler(repo)
		pr.Get("/projects/{id}/variables", vh.ListVariables)
		pr.Post("/projects/{id}/variables", vh.CreateVariable)
		pr.Get("/projects/{id}/variables/{varId}/edit", vh.EditVariable)
		pr.Put("/projects/{id}/variables/{varId}", vh.UpdateVariable)
		pr.Delete("/projects/{id}/variables/{varId}", vh.DeleteVariable)

		rh := handler.NewReleaseHandler(repo)
		pr.Get("/projects/{id}/releases", rh.ListReleases)
		pr.Post("/projects/{id}/releases", rh.CreateRelease)
		pr.Get("/projects/{id}/releases/{releaseId}", rh.GetRelease)
		pr.Post("/projects/{id}/releases/{releaseId}/refresh", rh.RefreshRelease)

		dh := handler.NewDeploymentHandler(repo, rnr)
		pr.Get("/deployments", dh.ListDeployments)
		pr.Post("/deployments", dh.CreateDeployment)
		pr.Get("/deployments/{id}", dh.GetDeployment)
		pr.Get("/deployments/{id}/status", dh.GetDeploymentStatus)
		pr.Post("/deployments/{id}/cancel", dh.CancelDeployment)
		pr.Post("/deployments/{id}/approve", dh.ApproveDeployment)
		pr.Post("/deployments/{id}/redeploy", dh.RedeployDeployment)
		pr.Get("/projects/{id}/deploy", dh.NewDeploymentPage)
		pr.Post("/projects/{id}/deploy", dh.ScheduleDeployment)

		sdh := handler.NewScheduledDeploymentHandler(repo, parser)
		pr.Get("/projects/{id}/schedules", sdh.List)
		pr.Get("/projects/{id}/schedules/new", sdh.NewForm)
		pr.Post("/projects/{id}/schedules", sdh.Create)
		pr.Get("/projects/{id}/schedules/{schedId}/edit", sdh.EditForm)
		pr.Put("/projects/{id}/schedules/{schedId}", sdh.Update)
		pr.Delete("/projects/{id}/schedules/{schedId}", sdh.Delete)
		pr.Post("/projects/{id}/schedules/{schedId}/toggle", sdh.Toggle)

		lh := handler.NewLogHandler(rnr.Broker(), repo)
		pr.Get("/deployments/{id}/logs/stream", lh.StreamLogs)
		pr.Get("/deployments/{id}/logs.txt", lh.ExportLogs)

		// Admin-only audit log viewer. RequireRole gates the sub-group
		// so non-admin roles get 403 on /admin/* without touching the
		// audit handler.
		pr.Group(func(ar chi.Router) {
			ar.Use(auth.RequireRole("admin"))
			adminH := handler.NewAdminHandler(repo)
			ar.Get("/admin/audit", adminH.ListAudit)
		})
	})

	return r
}
