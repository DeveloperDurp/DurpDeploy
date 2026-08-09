package handler_test

import (
	"regexp"
	"testing"
)

func TestMobile_Navbar_keeps_account_controls_in_right_aligned_menu(
	t *testing.T,
) {
	// Given
	fixture := newMobileStructuralFixture(t)

	// When
	adminBody := fixture.getHTML(t, fixture.admin, "/")
	viewerBody := fixture.getHTML(t, fixture.viewer, "/")

	// Then
	requireHTMLPattern(
		t,
		adminBody,
		`(?s)<details[^>]*data-mobile-nav[^>]*class="[^"]*dropdown-end[^"]*md:hidden[^"]*"`,
	)
	requireHTMLPattern(
		t,
		adminBody,
		`(?s)data-mobile-nav[^>]*@keydown.escape.window="\$el\.open = false"[^>]*@click.outside="\$el\.open = false"`,
	)
	requireHTMLPattern(
		t,
		adminBody,
		`(?s)data-mobile-nav[^>]*>.*?aria-label="Open navigation".*?@click="theme = theme === 'mocha' \? 'light' : 'mocha'".*?Test admin \(admin\).*?action="/logout"`,
	)
	requireHTMLPattern(
		t,
		adminBody,
		`(?s)data-mobile-nav[^>]*>.*?href="/admin/users"`,
	)
	requireHTMLPattern(
		t,
		viewerBody,
		`(?s)data-mobile-nav[^>]*>.*?Test viewer \(viewer\).*?action="/logout"`,
	)

	if regexp.MustCompile(`(?s)data-mobile-nav[^>]*>.*?href="/admin/users"`).
		MatchString(viewerBody) {
		t.Error("viewer received the mobile Admin link")
	}
	requireHTMLPattern(
		t,
		adminBody,
		`(?s)<div class="hidden[^"]*md:flex[^"]*">.*?action="/logout"`,
	)
	if !regexp.MustCompile(`data-mobile-nav="true"`).MatchString(adminBody) {
		t.Error("mobile navigation marker is absent")
	}
}
