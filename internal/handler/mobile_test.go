package handler_test

import (
	"fmt"
	"regexp"
	"testing"
)

func breakpointClassPattern(first, second string) string {
	return fmt.Sprintf(
		`class="[^"]*(?:%s[^"]*%s|%s[^"]*%s)[^"]*"`,
		regexp.QuoteMeta(first),
		regexp.QuoteMeta(second),
		regexp.QuoteMeta(second),
		regexp.QuoteMeta(first),
	)
}

func desktopVariantPattern(element string) string {
	return fmt.Sprintf(
		`(?s)<%s[^>]*%s`,
		regexp.QuoteMeta(element),
		breakpointClassPattern("hidden", "md:table"),
	)
}

func mobileVariantPattern(element string) string {
	return fmt.Sprintf(
		`(?s)<%s[^>]*class="[^"]*md:hidden[^"]*"`,
		regexp.QuoteMeta(element),
	)
}

func disclosurePattern(marker string) string {
	return fmt.Sprintf(
		`(?s)<details[^>]*data-disclosure="%s"[^>]*>\s*<summary(?:\s[^>]*)?>`,
		regexp.QuoteMeta(marker),
	)
}

func requireHTMLPattern(t *testing.T, body, pattern string) {
	t.Helper()
	matches, err := regexp.MatchString(pattern, body)
	if err != nil {
		t.Fatalf("compile pattern %q: %v", pattern, err)
	}
	if !matches {
		t.Errorf("missing rendered HTML marker %q", pattern)
	}
}

func TestMobile_RenderedHTML_includes_responsive_classes_when_authenticated(
	t *testing.T,
) {
	// Given
	fixture := newMobileStructuralFixture(t)

	pages := []struct {
		name     string
		path     string
		patterns []string
	}{
		{
			name: "home navbar",
			path: "/",
			patterns: []string{
				`(?s)<details[^>]*class="[^"]*xl:hidden[^"]*"`,
				`(?s)<thead class="hidden md:table-header-group">`,
				`(?s)<tr[^>]*class="[^"]*grid-cols-2[^"]*md:table-row[^"]*"`,
				`(?s)<span[^>]*class="[^"]*md:hidden[^"]*"[^>]*>Version</span>`,
			},
		},
		{
			name: "projects labeled phone cards",
			path: "/projects",
			patterns: []string{
				`(?s)<article[^>]*data-resource-card>.*?<a href="/projects/[0-9]+"[^>]*class="absolute inset-0`,
			},
		},
		{
			name: "deployment list labeled phone cards",
			path: "/deployments",
			patterns: []string{
				`(?s)<div[^>]*data-deployment-id=.*?<article[^>]*data-resource-card>`,
				`(?s)<dt[^>]*>Version</dt>`,
			},
		},
		{
			name: "deployment detail step log panels",
			path: fmt.Sprintf("/deployments/%d", fixture.deployment.ID),
			patterns: []string{
				`(?s)<section[^>]*aria-label="Step logs"[^>]*>.*?<details[^>]*data-step-index="0"[^>]*>\s*<summary[^>]*class="[^"]*break-words[^"]*"[^>]*>.*?Step 1:.*?deploy.*?</summary>`,
			},
		},
		{
			name: "admin users labeled phone cards",
			path: "/admin/users",
			patterns: []string{
				`(?s)<thead class="hidden lg:table-header-group">`,
				`(?s)<tr[^>]*class="[^"]*grid-cols-2[^"]*lg:table-row[^"]*"`,
				`(?s)<td[^>]*class="[^"]*break-all[^"]*"[^>]*data-user-email`,
				`(?s)<span[^>]*class="[^"]*lg:hidden[^"]*"[^>]*>Role</span>`,
				`(?s)<span[^>]*class="[^"]*lg:hidden[^"]*"[^>]*>Last login</span>`,
			},
		},
		{
			name: "templates card script preview",
			path: "/templates",
			patterns: []string{
				`(?s)<article[^>]*data-resource-card>.*?<pre[^>]*line-clamp-2`,
			},
		},
	}

	for _, page := range pages {
		t.Run(page.name, func(t *testing.T) {
			// When
			body := fixture.getHTML(t, fixture.admin, page.path)

			// Then
			for _, pattern := range page.patterns {
				requireHTMLPattern(t, body, pattern)
			}
		})
	}
}
