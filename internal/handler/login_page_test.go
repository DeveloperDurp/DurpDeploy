package handler_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLoginPage_PreservesPasswordForm_whenOIDCDisabled(t *testing.T) {
	// Given
	h := newAuthHarness(t)

	// When
	response, err := http.Get(h.server + "/login")
	if err != nil {
		t.Fatalf("get login page: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read login page: %v", err)
	}
	markup := string(body)

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	for _, required := range []string{
		`<form method="post" action="/login" class="space-y-4">`,
		`name="csrf_token" value=""`,
		`id="login-email" type="email" name="email" class="input input-bordered" autocomplete="username" required`,
		`id="login-password" type="password" name="password" class="input input-bordered" autocomplete="current-password" required`,
		`type="submit" class="btn btn-primary w-full">Login</button>`,
	} {
		if !strings.Contains(markup, required) {
			t.Errorf("disabled login page is missing %q", required)
		}
	}
	if strings.Contains(markup, `href="/login/oidc"`) ||
		strings.Contains(markup, "Sign in with") {
		t.Fatal("disabled login page rendered an OIDC affordance")
	}
}

func TestLoginPage_RendersAccessibleOIDCLink_whenEnabled(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	h.authHandler.SetOIDCDisplayName("Company SSO")

	// When
	response, err := http.Get(h.server + "/login")
	if err != nil {
		t.Fatalf("get login page: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read login page: %v", err)
	}
	markup := string(body)

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	const oidcLink = `<a href="/login/oidc" class="btn btn-outline w-full" aria-label="Sign in with Company SSO">Sign in with Company SSO</a>`
	if !strings.Contains(markup, oidcLink) {
		t.Fatalf(
			"enabled login page is missing accessible OIDC link %q",
			oidcLink,
		)
	}
	if strings.Index(
		markup,
		`type="submit" class="btn btn-primary w-full">Login`,
	) >
		strings.Index(
			markup,
			oidcLink,
		) {
		t.Fatal("OIDC link precedes the password form submit action")
	}
}

func TestLoginPage_AssociatesCredentialLabelsAndAutocomplete(t *testing.T) {
	// Given
	h := newAuthHarness(t)

	// When
	response, err := http.Get(h.server + "/login")
	if err != nil {
		t.Fatalf("get login page: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read login page: %v", err)
	}
	markup := string(body)

	// Then
	for _, required := range []string{
		`<label class="label" for="login-email">`,
		`id="login-email" type="email" name="email" class="input input-bordered" autocomplete="username" required`,
		`<label class="label" for="login-password">`,
		`id="login-password" type="password" name="password" class="input input-bordered" autocomplete="current-password" required`,
	} {
		if !strings.Contains(markup, required) {
			t.Errorf(
				"login page is missing accessible credential markup %q",
				required,
			)
		}
	}
}

func TestLoginPage_EscapesOIDCDisplayName_whenConfiguredNameIsHTML(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	const displayName = `<img src=x onerror="provider diagnostic">`
	h.authHandler.SetOIDCDisplayName(displayName)

	// When
	response, err := http.Get(h.server + "/login")
	if err != nil {
		t.Fatalf("get login page: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read login page: %v", err)
	}
	markup := string(body)

	// Then
	if strings.Contains(markup, displayName) ||
		strings.Contains(markup, `<img src=x onerror=`) {
		t.Fatalf("login page rendered raw OIDC display HTML: %s", markup)
	}
	if !strings.Contains(markup, "Sign in with &lt;img") {
		t.Fatalf("login page did not escape the OIDC display name: %s", markup)
	}
}

func TestLoginPage_RendersOnlyGenericError_whenOIDCIsEnabled(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	h.authHandler.SetOIDCDisplayName("Company SSO")

	// When
	response, err := http.PostForm(
		h.server+"/login",
		map[string][]string{
			"email":    {"missing@example.com"},
			"password": {"wrong"},
		},
	)
	if err != nil {
		t.Fatalf("post invalid password login: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read invalid password login: %v", err)
	}
	markup := string(body)

	// Then
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf(
			"status = %d, want %d",
			response.StatusCode,
			http.StatusUnprocessableEntity,
		)
	}
	if !strings.Contains(markup, "Invalid email or password") {
		t.Fatalf("login page omitted the generic error: %s", markup)
	}
	if !strings.Contains(
		markup,
		`<div class="alert alert-error mb-4" role="alert" aria-live="assertive">`,
	) {
		t.Fatalf("login page did not announce the generic error: %s", markup)
	}
	if strings.Contains(markup, "provider diagnostic") {
		t.Fatalf("login page leaked provider detail: %s", markup)
	}
}
