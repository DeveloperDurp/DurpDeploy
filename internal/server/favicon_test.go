package server

import (
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func Test_NewRouter_serves_favicon_when_requested(t *testing.T) {
	// Given
	harness := newOIDCRouterHarness(t)
	router := NewRouter(
		harness.repo,
		harness.runner,
		harness.parser,
		harness.authHandler,
	)

	// When
	redirect := httptest.NewRecorder()
	router.ServeHTTP(
		redirect,
		httptest.NewRequest(http.MethodGet, "/favicon.ico", nil),
	)

	// Then
	if redirect.Code != http.StatusPermanentRedirect {
		t.Fatalf(
			"favicon status = %d, want %d",
			redirect.Code,
			http.StatusPermanentRedirect,
		)
	}
	const faviconPath = "/static/icons/favicon-64.png"
	if location := redirect.Header().Get("Location"); location != faviconPath {
		t.Fatalf("favicon location = %q, want %q", location, faviconPath)
	}

	asset := httptest.NewRecorder()
	router.ServeHTTP(
		asset,
		httptest.NewRequest(http.MethodGet, faviconPath, nil),
	)
	if asset.Code != http.StatusOK {
		t.Fatalf(
			"favicon asset status = %d, want %d",
			asset.Code,
			http.StatusOK,
		)
	}
	config, err := png.DecodeConfig(asset.Body)
	if err != nil {
		t.Fatalf("decode favicon asset: %v", err)
	}
	if config.Width != 64 || config.Height != 64 {
		t.Fatalf(
			"favicon dimensions = %dx%d, want 64x64",
			config.Width,
			config.Height,
		)
	}
}
