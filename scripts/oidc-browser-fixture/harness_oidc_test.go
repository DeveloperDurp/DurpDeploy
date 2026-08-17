//go:build oidctest

package main

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBrowserFixture_ExposesOIDCLogin_whenStarted(t *testing.T) {
	// Given
	binary := buildHarness(t)
	readyFile := t.TempDir() + "/ready.json"
	_, done := startHarness(t, binary, readyFile)
	state := waitForReadiness(t, readyFile, done)
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: time.Second,
	}

	// When
	loginResponse, err := client.Get(state.AppURL + "/login")
	if err != nil {
		t.Fatalf("get fixture login page: %v", err)
	}
	defer loginResponse.Body.Close()
	loginPage, err := io.ReadAll(loginResponse.Body)
	if err != nil {
		t.Fatalf("read fixture login page: %v", err)
	}
	startResponse, err := client.Get(state.AppURL + "/login/oidc")
	if err != nil {
		t.Fatalf("start fixture OIDC login: %v", err)
	}
	defer startResponse.Body.Close()

	// Then
	if !strings.Contains(string(loginPage), `href="/login/oidc"`) {
		t.Fatal("fixture login page does not expose the OIDC link")
	}
	if startResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"fixture OIDC start status = %d, want %d",
			startResponse.StatusCode,
			http.StatusSeeOther,
		)
	}
}

func TestBrowserFixture_StopsOnlyFakeIDP_whenOutageRequested(t *testing.T) {
	// Given
	binary := buildHarness(t)
	readyFile := t.TempDir() + "/ready.json"
	_, done := startHarness(t, binary, readyFile, "--outage")
	state := waitForReadiness(t, readyFile, done)
	client := &http.Client{Timeout: time.Second}

	// When
	healthResponse, err := client.Get(state.AppURL + "/healthz")
	if err != nil {
		t.Fatalf("get fixture health during outage: %v", err)
	}
	defer healthResponse.Body.Close()
	startResponse, err := client.Get(state.AppURL + "/login/oidc")
	if err != nil {
		t.Fatalf("start fixture OIDC login during outage: %v", err)
	}
	defer startResponse.Body.Close()
	loginPage, err := io.ReadAll(startResponse.Body)
	if err != nil {
		t.Fatalf("read fixture outage login page: %v", err)
	}
	idpURL, err := url.Parse(state.IDPURL)
	if err != nil {
		t.Fatalf("parse fixture IdP URL: %v", err)
	}
	connection, dialErr := net.DialTimeout("tcp", idpURL.Host, time.Second)
	if connection != nil {
		connection.Close()
	}

	// Then
	if healthResponse.StatusCode != http.StatusOK {
		t.Fatalf(
			"fixture health during outage = %d, want %d",
			healthResponse.StatusCode,
			http.StatusOK,
		)
	}
	if startResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf(
			"fixture OIDC outage status = %d, want %d",
			startResponse.StatusCode,
			http.StatusServiceUnavailable,
		)
	}
	if dialErr == nil {
		t.Fatal("fixture outage left the fake IdP listening")
	}
	if !strings.Contains(
		string(loginPage),
		"Single sign-on is temporarily unavailable",
	) {
		t.Fatal("fixture outage did not render the generic retry message")
	}
	if !strings.Contains(string(loginPage), `type="password"`) {
		t.Fatal("fixture outage did not preserve the password fallback form")
	}
}
