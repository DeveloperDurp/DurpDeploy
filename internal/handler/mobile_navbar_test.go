package handler_test

import (
	"regexp"
	"strings"
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
	securityBody := fixture.getHTML(t, fixture.admin, "/settings/security")

	// Then
	requireHTMLPattern(
		t,
		adminBody,
		`(?s)<details[^>]*data-mobile-nav[^>]*data-focus-menu`+
			`[^>]*class="[^"]*dropdown-end[^"]*md:hidden[^"]*"`,
	)
	requireHTMLPattern(
		t,
		adminBody,
		`(?s)x-data="navbar"[^>]*@keydown.escape.window=`+
			`"closeFocusedMenu\(\$event\)"[^>]*@click.window=`+
			`"closeOutsideMenu\(\$event\)"`,
	)
	requireHTMLPattern(
		t,
		adminBody,
		`(?s)data-mobile-nav[^>]*>.*?aria-label="Open navigation"`,
	)
	requireHTMLPattern(t, adminBody, `(?s)href="/admin/users"`)
	requireHTMLPattern(
		t,
		adminBody,
		`(?s)<details[^>]*data-account-menu[^>]*>.*?<summary[^>]*>`+
			`\s*Test admin \(admin\)\s*</summary>.*?href="/settings/security"`+
			`.*?href="/settings/tokens".*?<li[^>]*class="menu-title"[^>]*>`+
			`\s*<hr>\s*</li>.*?action="/logout"`,
	)
	requireHTMLPattern(
		t,
		viewerBody,
		`(?s)<details[^>]*data-account-menu[^>]*>.*?<summary[^>]*>`+
			`\s*Test viewer \(viewer\)\s*</summary>.*?href="/settings/security"`+
			`.*?action="/logout"`,
	)
	logoutForms := regexp.MustCompile(
		`(?s)action="/logout">.*?name="csrf_token"`,
	).FindAllStringIndex(adminBody, -1)
	if len(logoutForms) != 2 {
		t.Error(
			"account logout forms must include CSRF tokens for no-JS submission",
		)
	}
	if regexp.MustCompile(`(?s)data-mobile-nav[^>]*>.*?href="/admin/users"`).
		MatchString(viewerBody) {
		t.Error("viewer received the mobile Admin link")
	}
	if regexp.MustCompile(`(?s)data-account-menu[^>]*>.*?href="/settings/tokens"`).
		MatchString(viewerBody) {
		t.Error("viewer received the account Tokens link")
	}
	if strings.Count(adminBody, `href="/settings/tokens"`) != 2 {
		t.Error("Tokens is not limited to the desktop and mobile account menus")
	}
	requireHTMLPattern(
		t,
		securityBody,
		`(?s)<details[^>]*data-account-menu[^>]*>.*?<summary[^>]*`+
			`class="[^"]*active[^"]*"[^>]*>\s*Test admin \(admin\)\s*</summary>`,
	)
	requireHTMLPattern(
		t,
		adminBody,
		`(?s)<details[^>]*class="[^"]*dropdown-end[^"]*hidden`+
			`[^"]*md:block[^"]*"[^>]*>.*?>Admin</summary>`,
	)
	requireHTMLPattern(
		t,
		adminBody,
		`(?s)<button class="btn btn-ghost btn-sm" @click=`+
			`"theme = theme === 'mocha' \? 'light' : 'mocha'"`,
	)
	if !regexp.MustCompile(`data-mobile-nav="true"`).MatchString(adminBody) {
		t.Error("mobile navigation marker is absent")
	}
}

func TestMobileNavbar_uses_single_line_shrink_safe_layout(t *testing.T) {
	// Given
	fixture := newMobileStructuralFixture(t)

	// When
	body := fixture.getHTML(t, fixture.admin, "/")

	// Then
	requireHTMLPattern(
		t,
		body,
		`(?s)<div class="w-full px-4 sm:px-6 lg:px-8 flex flex-nowrap items-center gap-2">`,
	)
	requireHTMLPattern(
		t,
		body,
		`(?s)<div class="flex min-w-0 flex-1 items-center gap-2">`,
	)
	requireHTMLPattern(
		t,
		body,
		`(?s)<div class="hidden min-w-0 flex-1 overflow-x-auto md:flex">`+
			`.*?<ul class="menu menu-horizontal flex-nowrap whitespace-nowrap px-1">`,
	)
}

func TestAccountDropdown_marks_each_open_menu_for_scoped_dismissal(
	t *testing.T,
) {
	// Given
	fixture := newMobileStructuralFixture(t)

	// When
	body := fixture.getHTML(t, fixture.admin, "/")

	// Then
	requireHTMLPattern(
		t,
		body,
		`(?s)<details data-focus-menu class="dropdown dropdown-end hidden shrink-0 md:block">`+
			`.*?<summary class="btn btn-ghost btn-sm">Admin</summary>`,
	)
	requireHTMLPattern(
		t,
		body,
		`(?s)<details data-focus-menu>\s*<summary>Admin</summary>`,
	)
}
