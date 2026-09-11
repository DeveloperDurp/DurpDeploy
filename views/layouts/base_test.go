package layouts

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func Test_Base_advertises_favicon_in_document_head(t *testing.T) {
	// Given
	var output bytes.Buffer

	// When
	err := Base("DurpDeploy", "/login").Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render base layout: %v", err)
	}

	// Then
	const faviconLink = `<link rel="icon" type="image/png" sizes="64x64" href="/static/icons/favicon-64.png">`
	if !strings.Contains(output.String(), faviconLink) {
		t.Fatalf("base layout is missing favicon link %q", faviconLink)
	}
}

func TestBaseLayoutUsesSingleAlpineEntryPoint(t *testing.T) {
	// Given
	var output bytes.Buffer

	// When
	err := Base("DurpDeploy", "/login").Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render base layout: %v", err)
	}
	rendered := output.String()

	// Then
	if count := strings.Count(rendered, `/static/js/app.bundle.js`); count != 1 {
		t.Fatalf("base layout references active Alpine entry %d times, want 1", count)
	}
	if strings.Contains(rendered, `/static/js/alpine.bundle.js`) {
		t.Fatal("base layout references orphan Alpine entry")
	}
	if _, err := os.Stat("../../static/js/alpine.bundle.js"); !os.IsNotExist(err) {
		t.Fatalf("orphan Alpine bundle still exists: %v", err)
	}
	if !strings.Contains(
		rendered,
		`<script src="/static/js/app.bundle.js" defer></script>`,
	) {
		t.Fatal("active Alpine entry is not deferred")
	}
	themeBootstrap := strings.Index(rendered, `localStorage.getItem('theme')`)
	if themeBootstrap < 0 {
		t.Fatal("theme bootstrap marker is missing")
	}
	activeEntry := strings.Index(rendered, `<script src="/static/js/app.bundle.js"`)
	if activeEntry < 0 {
		t.Fatal("active Alpine entry marker is missing")
	}
	if themeBootstrap > activeEntry {
		t.Fatal("theme bootstrap must precede deferred Alpine entry")
	}
	csrfBootstrap := strings.Index(rendered, `htmx:configRequest`)
	if csrfBootstrap < 0 {
		t.Fatal("CSRF bootstrap marker is missing")
	}
	if csrfBootstrap > activeEntry {
		t.Fatal("CSRF bootstrap must precede deferred Alpine entry")
	}
}
