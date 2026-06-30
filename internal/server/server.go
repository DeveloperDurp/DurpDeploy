package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

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

func NewRouter(repo *repository.Repository, rnr *runner.DeploymentRunner) *chi.Mux {
	r := chi.NewRouter()
	r.Use(requestLogger)
	r.Use(handler.PanicRecoveryMiddleware)

	// Serve static files from embedded assets
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(static.Assets))))

	errorHandler := handler.NewErrorHandler()
	r.NotFound(errorHandler.NotFound)
	r.MethodNotAllowed(errorHandler.MethodNotAllowed)

	// Home page
	indexHandler := handler.NewIndexHandler(repo)
	r.Get("/", indexHandler.Index)

	envHandler := handler.NewEnvironmentHandler(repo)
	r.Get("/environments", envHandler.ListEnvironments)
	r.Get("/environments/new", envHandler.NewEnvironment)
	r.Post("/environments", envHandler.CreateEnvironment)
	r.Get("/environments/{id}/edit", envHandler.EditEnvironment)
	r.Put("/environments/{id}", envHandler.UpdateEnvironment)
	r.Delete("/environments/{id}", envHandler.DeleteEnvironment)

	ph := handler.NewProjectHandler(repo)
	r.Get("/projects", ph.ListProjects)
	r.Get("/projects/new", ph.NewProject)
	r.Post("/projects", ph.CreateProject)
	r.Get("/projects/{id}", ph.GetProject)
	r.Get("/projects/{id}/edit", ph.EditProject)
	r.Put("/projects/{id}", ph.UpdateProject)
	r.Delete("/projects/{id}", ph.DeleteProject)

	sh := handler.NewStepHandler(repo)
	r.Get("/projects/{id}/steps", sh.ListSteps)
	r.Get("/projects/{id}/steps/new", sh.NewStepForm)
	r.Post("/projects/{id}/steps", sh.CreateStep)
	r.Get("/projects/{id}/steps/{stepId}/edit", sh.EditStepForm)
	r.Put("/projects/{id}/steps/{stepId}", sh.UpdateStep)
	r.Delete("/projects/{id}/steps/{stepId}", sh.DeleteStep)
	r.Patch("/projects/{id}/steps/reorder", sh.ReorderStep)

	sth := handler.NewStepTemplateHandler(repo)
	r.Get("/templates", sth.ListTemplates)
	r.Get("/templates/new", sth.NewTemplateForm)
	r.Post("/templates", sth.CreateTemplate)
	r.Get("/templates/{id}/edit", sth.EditTemplateForm)
	r.Put("/templates/{id}", sth.UpdateTemplate)
	r.Delete("/templates/{id}", sth.DeleteTemplate)
	r.Get("/projects/{id}/templates-picker", sth.TemplatesPicker)
	r.Post("/projects/{id}/steps/from-template/{templateId}", sth.InsertTemplate)
	r.Post("/projects/{id}/steps/{stepId}/save-as-template", sth.SaveStepAsTemplate)

	vh := handler.NewVariableHandler(repo)
	r.Get("/projects/{id}/variables", vh.ListVariables)
	r.Post("/projects/{id}/variables", vh.CreateVariable)
	r.Get("/projects/{id}/variables/{varId}/edit", vh.EditVariable)
	r.Put("/projects/{id}/variables/{varId}", vh.UpdateVariable)
	r.Delete("/projects/{id}/variables/{varId}", vh.DeleteVariable)

	rh := handler.NewReleaseHandler(repo)
	r.Get("/projects/{id}/releases", rh.ListReleases)
	r.Post("/projects/{id}/releases", rh.CreateRelease)
	r.Get("/projects/{id}/releases/{releaseId}", rh.GetRelease)
	r.Post("/projects/{id}/releases/{releaseId}/refresh", rh.RefreshRelease)

	dh := handler.NewDeploymentHandler(repo, rnr)
	r.Get("/deployments", dh.ListDeployments)
	r.Post("/deployments", dh.CreateDeployment)
	r.Get("/deployments/{id}", dh.GetDeployment)
	r.Get("/deployments/{id}/status", dh.GetDeploymentStatus)
	r.Post("/deployments/{id}/cancel", dh.CancelDeployment)

	lh := handler.NewLogHandler(rnr.Broker(), repo)
	r.Get("/deployments/{id}/logs/stream", lh.StreamLogs)

	return r
}
