package handler_test

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/mfa"
)

const loginContextCookie = "login_destination"

func TestLoginContext_ReturnsToRequestedPage(t *testing.T) {
	h := newAuthHarness(t)
	user := h.seedUser(t, "login-context@example.test", "hunter2")
	client := newJar(t)
	const target = "/projects?name=login-context"
	response, err := client.Get(h.server + target)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	location := response.Header.Get("Location")
	parsed, err := url.Parse(location)
	if err != nil || parsed.Path != "/login" ||
		parsed.Query().Get("next") != target {
		t.Fatalf("login redirect = %q", location)
	}
	page, err := client.Get(h.server + location)
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	response, err = client.PostForm(h.server+"/login", url.Values{
		"email": {user.Email}, "password": {"hunter2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != target {
		t.Fatalf("successful login = %d %q", response.StatusCode,
			response.Header.Get("Location"))
	}
	assertLoginDestinationCleared(t, response)
}

func TestLoginContext_RetainsOnlyEmailAfterFailure(t *testing.T) {
	h := newAuthHarness(t)
	client := newJar(t)
	response, err := client.PostForm(h.server+"/login", url.Values{
		"email":    {"missing+login@example.test"},
		"password": {"incorrect-test-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body := readBody(t, response)
	if response.StatusCode != http.StatusUnprocessableEntity ||
		!strings.Contains(body, `value="missing+login@example.test"`) {
		t.Fatal("failed login discarded the email")
	}
	if strings.Contains(body, "incorrect-test-password") {
		t.Fatal("failed login retained the password")
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("login error can be cached")
	}
}

func TestLoginContext_TamperedCookieCannotRedirectOutside(t *testing.T) {
	h := newAuthHarness(t)
	user := h.seedUser(t, "cookie-login@example.test", "hunter2")
	for _, value := range []string{
		"invalid%%%", base64.RawURLEncoding.EncodeToString([]byte("//evil.test")),
		strings.Repeat("a", 4097),
	} {
		client := newJar(t)
		serverURL, err := url.Parse(h.server)
		if err != nil {
			t.Fatal(err)
		}
		client.Jar.SetCookies(serverURL, []*http.Cookie{
			{Name: loginContextCookie, Value: value, Path: "/"},
		})
		response, err := client.PostForm(h.server+"/login", url.Values{
			"email": {user.Email}, "password": {"hunter2"},
		})
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.Header.Get("Location") != "/" {
			t.Fatal("tampered destination changed the login target")
		}
		assertLoginDestinationCleared(t, response)
	}
}

func TestLoginContext_ExplicitLoginClearsOldDestination(t *testing.T) {
	h := newAuthHarness(t)
	user := h.seedUser(t, "plain-login@example.test", "hunter2")
	client := newJar(t)
	for _, path := range []string{"/login?next=%2Fprojects", "/login"} {
		response, err := client.Get(h.server + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	response, err := client.PostForm(h.server+"/login", url.Values{
		"email": {user.Email}, "password": {"hunter2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.Header.Get("Location") != "/" {
		t.Fatal("plain login reused an old destination")
	}
}

func assertLoginDestinationCleared(t *testing.T, response *http.Response) {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == loginContextCookie && cookie.MaxAge < 0 &&
			cookie.Value == "" && cookie.HttpOnly &&
			cookie.SameSite == http.SameSiteLaxMode {
			return
		}
	}
	t.Fatal("successful login retained its destination cookie")
}

func TestLoginContext_OIDCReturnsToRequestedPage(t *testing.T) {
	h := newAuthHarness(t)
	fixture := configureOIDCCallback(t, h)
	flow := fixture.begin(t, h)
	page, err := http.Get(h.server + "/login?next=%2Fprojects")
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	var destination *http.Cookie
	for _, cookie := range page.Cookies() {
		if cookie.Name == loginContextCookie {
			destination = cookie
		}
	}
	if destination == nil {
		t.Fatal("login page did not remember the destination")
	}
	response := flow.callback(t, destination)
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/projects" ||
		!hasCookie(response.Cookies(), "session") {
		t.Fatal("SSO completion discarded the login destination")
	}
	assertLoginDestinationCleared(t, response)
}

func TestLoginContext_EscapesRetainedEmail(t *testing.T) {
	h := newAuthHarness(t)
	response, err := http.PostForm(h.server+"/login", url.Values{
		"email":    {`"><svg onload=alert(1)>@example.test`},
		"password": {"incorrect-test-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body := readBody(t, response)
	if strings.Contains(body, "<svg onload=alert(1)>") ||
		!strings.Contains(body, "&lt;svg onload=alert(1)&gt;") {
		t.Fatal("retained email did not escape markup")
	}
}

func TestLoginContext_DestinationCookieUsesSessionSecurityPolicy(t *testing.T) {
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{CookieSecure: true})
	response, err := http.Get(h.server + "/login?next=%2Fprojects")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	for _, cookie := range response.Cookies() {
		if cookie.Name != loginContextCookie {
			continue
		}
		if !cookie.HttpOnly || !cookie.Secure || cookie.Path != "/" ||
			cookie.SameSite != http.SameSiteLaxMode ||
			cookie.MaxAge <= 0 || !cookie.Expires.After(time.Now()) {
			t.Fatal("destination cookie omitted session security controls")
		}
		return
	}
	t.Fatal("destination cookie is missing")
}
