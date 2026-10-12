package static

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"net/http"
	"strings"
)

//go:embed js css icons/favicon-64.png swagger-ui
var Assets embed.FS

var versions = map[string]string{
	"css/tailwind.min.css": assetVersion("css/tailwind.min.css"),
	"js/app.bundle.js":     assetVersion("js/app.bundle.js"),
	"js/charts.bundle.js":  assetVersion("js/charts.bundle.js"),
}

func assetVersion(name string) string {
	content, err := Assets.ReadFile(name)
	if err != nil {
		// Leave a missing asset uncached; FileServer reports the missing file.
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(content))
}

func URL(name string) string {
	path := "/static/" + name
	if version := versions[name]; version != "" {
		path += "?v=" + version
	}
	return path
}

func Handler() http.Handler {
	files := http.FileServer(http.FS(Assets))
	return http.StripPrefix(
		"/static/",
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if version := versions[strings.TrimPrefix(r.URL.Path, "/")]; version != "" {
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("ETag", `"`+version+`"`)
				if requested := r.URL.Query().Get("v"); requested != "" {
					if requested != version {
						w.Header().Set("Cache-Control", "no-store")
						http.NotFound(w, r)
						return
					}
					w.Header().
						Set("Cache-Control", "public, max-age=31536000, immutable")
				}
			}
			files.ServeHTTP(w, r)
		}),
	)
}
