package api

import (
	"log/slog"
	"net/http"

	"github.com/go-openapi/runtime/middleware"

	"durpdeploy/internal/swagger"
)

// SwaggerHandler serves the generated OpenAPI spec and swagger-ui.
type SwaggerHandler struct{}

// NewSwaggerHandler creates a new SwaggerHandler.
func NewSwaggerHandler() *SwaggerHandler {
	return &SwaggerHandler{}
}

// Spec serves the generated OpenAPI 2.0 spec as JSON.
func (h *SwaggerHandler) Spec(w http.ResponseWriter, r *http.Request) {
	spec, err := swagger.ReadSpec()
	if err != nil {
		slog.Error("failed to read swagger spec", "error", err)
		RespondError(w, http.StatusInternalServerError, "spec not available")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(spec)
}

const swaggerUITemplate = `<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="UTF-8">
    <title>{{ .Title }}</title>
    {{ if .SwaggerStylesURL }}<link rel="stylesheet" type="text/css" href="{{ .SwaggerStylesURL }}" />{{ end }}
    {{ if .Favicon32 }}<link rel="icon" type="image/png" href="{{ .Favicon32 }}" sizes="32x32" />{{ end }}
    {{ if .Favicon16 }}<link rel="icon" type="image/png" href="{{ .Favicon16 }}" sizes="16x16" />{{ end }}
    <style>
      html { box-sizing: border-box; overflow: -moz-scrollbars-vertical; overflow-y: scroll; }
      *, *:before, *:after { box-sizing: inherit; }
      body { margin: 0; background: #fafafa; }
    </style>
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="{{ .AssetsURL }}"></script>
    {{ if .SwaggerPresetURL }}<script src="{{ .SwaggerPresetURL }}"></script>{{ end }}
    <script>
      // requestInterceptor: the spec declares the bearer token as an
      // apiKey in the Authorization header. Users paste the raw token
      // (e.g. "ddp_pat_…") into the Authorize dialog; we prepend
      // "Bearer " here so the wire header is the canonical
      // "Authorization: Bearer <token>" the server expects.
      function durpdeployAuthInterceptor(req) {
        const auth = req.headers && req.headers['Authorization'];
        if (typeof auth === 'string' && auth.length > 0 &&
            !/^Bearer\s/i.test(auth) && !/^Basic\s/i.test(auth)) {
          req.headers['Authorization'] = 'Bearer ' + auth;
        }
        return req;
      }
      window.onload = function() {
        const ui = SwaggerUIBundle({
          url: '{{ .SpecURL }}',
          dom_id: '#swagger-ui',
          deepLinking: true,
          presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
          plugins: [SwaggerUIBundle.plugins.DownloadUrl],
          requestInterceptor: durpdeployAuthInterceptor,
          layout: "StandaloneLayout"
        });
        window.ui = ui;
      };
    </script>
  </body>
</html>`

// UI serves the swagger-ui HTML page.
func (h *SwaggerHandler) UI(w http.ResponseWriter, r *http.Request) {
	middleware.SwaggerUI(middleware.SwaggerUIOpts{
		BasePath:         "/api/swagger",
		Path:             "index.html",
		SpecURL:          "/api/swagger/spec",
		Title:            "DurpDeploy API",
		Template:         swaggerUITemplate,
		SwaggerURL:       "/static/swagger-ui/swagger-ui-bundle.js",
		SwaggerPresetURL: "/static/swagger-ui/swagger-ui-standalone-preset.js",
		SwaggerStylesURL: "/static/swagger-ui/swagger-ui.css",
		Favicon32:        "/static/swagger-ui/favicon-32x32.png",
		Favicon16:        "/static/swagger-ui/favicon-16x16.png",
	}, nil).ServeHTTP(w, r)
}

// SpecResponse is the raw OpenAPI JSON spec.
// swagger:model SpecResponse
type swaggerSpecResponse struct {
	// swagger:ignore
	Body []byte
}

// SwaggerUIResponse is the swagger-ui HTML page.
// swagger:model SwaggerUIResponse
type swaggerUIResponse struct {
	// swagger:ignore
	Body string
}
