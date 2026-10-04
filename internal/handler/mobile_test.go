package handler_test

import (
	"fmt"
	"regexp"
	"testing"
)

const mdTableCell = "md:table-cell"

func breakpointClassPattern(first, second string) string {
	return fmt.Sprintf(
		`class="[^"]*(?:%s[^"]*%s|%s[^"]*%s)[^"]*"`,
		regexp.QuoteMeta(first),
		regexp.QuoteMeta(second),
		regexp.QuoteMeta(second),
		regexp.QuoteMeta(first),
	)
}

func responsiveHeaderPattern(breakpoint string) string {
	return fmt.Sprintf(
		`(?s)<th[^>]*%s`,
		breakpointClassPattern("hidden", breakpoint),
	)
}

func responsiveCellPattern(breakpoint string) string {
	return fmt.Sprintf(
		`(?s)<td[^>]*%s`,
		breakpointClassPattern("hidden", breakpoint),
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
				responsiveHeaderPattern(mdTableCell),
				responsiveCellPattern(mdTableCell),
			},
		},
		{
			name: "projects description column",
			path: "/projects",
			patterns: []string{
				responsiveHeaderPattern(mdTableCell),
				responsiveCellPattern(mdTableCell),
			},
		},
		{
			name: "deployment list labeled phone cards",
			path: "/deployments",
			patterns: []string{
				responsiveHeaderPattern(mdTableCell),
				`(?s)<tr[^>]*class="[^"]*grid-cols-2[^"]*md:table-row[^"]*"[^>]*data-deployment-id=`,
				`(?s)<span[^>]*class="[^"]*md:hidden[^"]*"[^>]*>Version</span>`,
			},
		},
		{
			name: "deployment detail step cards and table",
			path: fmt.Sprintf("/deployments/%d", fixture.deployment.ID),
			patterns: []string{
				fmt.Sprintf(
					`(?s)<table[^>]*%s`,
					breakpointClassPattern("hidden", "sm:table"),
				),
				`(?s)<div[^>]*class="[^"]*sm:hidden[^"]*"[^>]*>\s*<details[^>]*>\s*<summary[^>]*>.*?Step 1: deploy.*?</summary>\s*<pre[^>]*>echo deploy</pre>`,
			},
		},
		{
			name: "admin users last login column",
			path: "/admin/users",
			patterns: []string{
				responsiveHeaderPattern(mdTableCell),
				responsiveCellPattern(mdTableCell),
			},
		},
		{
			name: "templates script disclosure",
			path: "/templates",
			patterns: []string{
				disclosurePattern(
					fmt.Sprintf("template-script-%d", fixture.template.ID),
				),
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
