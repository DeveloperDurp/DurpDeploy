package handler_test

import (
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/oidc"
	"durpdeploy/internal/oidc/oidctest"
	"durpdeploy/internal/secret"
)

const (
	oidcCallbackFailureMessage = "Single sign-on could not be completed"
	oidcCallbackUserAgent      = "oidc-callback-handler-test"
)

type oidcCallbackFixture struct {
	provider *oidctest.Fixture
	codec    *oidc.TransactionCookieCodec
}

type oidcCallbackFlow struct {
	fixture           oidcCallbackFixture
	callbackURL       string
	transactionCookie *http.Cookie
}

func configureOIDCCallback(
	t *testing.T,
	h *authHarness,
) oidcCallbackFixture {
	t.Helper()
	providerFixture, err := oidctest.New(oidctest.Options{})
	if err != nil {
		t.Fatalf("new OIDC fixture: %v", err)
	}
	t.Cleanup(providerFixture.Close)

	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new transaction secret box: %v", err)
	}
	codec, err := oidc.NewTransactionCookieCodec(
		box,
		oidc.TransactionCookieConfig{
			Secure: true,
			Now:    providerFixture.Now,
		},
	)
	if err != nil {
		t.Fatalf("new transaction cookie codec: %v", err)
	}
	transactions, err := oidc.NewTransactionStore(oidc.TransactionStoreOptions{
		Repository: h.repo, CookieCodec: codec,
	})
	if err != nil {
		t.Fatalf("new OIDC transaction store: %v", err)
	}
	provider, err := oidc.NewProvider(oidc.ProviderOptions{
		Config: oidc.Config{
			Enabled:       true,
			Issuer:        providerFixture.URL(),
			ClientID:      providerFixture.ClientID(),
			ClientSecret:  providerFixture.ClientSecret(),
			CallbackURL:   "https://deploy.example/login/oidc/callback",
			Scopes:        []string{"openid", "profile", "email"},
			GroupClaim:    "groups",
			AdminGroup:    "fixture-admin",
			DeployerGroup: "fixture-deployer",
			ViewerGroup:   "fixture-viewer",
		},
		HTTPClient: providerFixture.Client(),
		Now:        providerFixture.Now,
	})
	if err != nil {
		t.Fatalf("new OIDC provider: %v", err)
	}
	h.authHandler.SetOIDCLogin(provider, transactions)
	return oidcCallbackFixture{provider: providerFixture, codec: codec}
}

func (f oidcCallbackFixture) begin(
	t *testing.T,
	h *authHarness,
) oidcCallbackFlow {
	t.Helper()
	loginServer := startOIDCLoginServer(t, h)
	callbackServer := httptest.NewServer(
		http.HandlerFunc(h.authHandler.LoginOIDCCallbackGet),
	)
	t.Cleanup(callbackServer.Close)

	startClient := newJar(t)
	startResponse, err := startClient.Get(loginServer.URL + "/login/oidc")
	if err != nil {
		t.Fatalf("start OIDC login: %v", err)
	}
	defer startResponse.Body.Close()
	if startResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("OIDC start = %d, want 303", startResponse.StatusCode)
	}
	transactionCookie := oidcTransactionCookie(t, startResponse.Cookies())

	providerClient := *f.provider.Client()
	providerClient.CheckRedirect = func(
		_ *http.Request,
		_ []*http.Request,
	) error {
		return http.ErrUseLastResponse
	}
	authorizeResponse, err := providerClient.Get(
		startResponse.Header.Get("Location"),
	)
	if err != nil {
		t.Fatalf("authorize OIDC login: %v", err)
	}
	defer authorizeResponse.Body.Close()
	if authorizeResponse.StatusCode != http.StatusFound {
		t.Fatalf("OIDC authorize = %d, want 302", authorizeResponse.StatusCode)
	}
	callback, err := url.Parse(authorizeResponse.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse OIDC callback URL: %v", err)
	}
	return oidcCallbackFlow{
		fixture:           f,
		callbackURL:       callbackServer.URL + callback.RequestURI(),
		transactionCookie: transactionCookie,
	}
}

func (f oidcCallbackFlow) callback(t *testing.T) *http.Response {
	t.Helper()
	response, err := doOIDCCallback(f.callbackURL, f.transactionCookie)
	if err != nil {
		t.Fatalf("call OIDC callback: %v", err)
	}
	return response
}

func doOIDCCallback(
	callbackURL string,
	transactionCookie *http.Cookie,
) (*http.Response, error) {
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		callbackURL,
		nil,
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", oidcCallbackUserAgent)
	request.AddCookie(transactionCookie)
	client := http.Client{
		Timeout: time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return client.Do(request)
}

func assertOIDCCallbackSuccess(t *testing.T, response *http.Response) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/" {
		t.Fatalf(
			"OIDC callback response = %d %q, want 303 to /",
			response.StatusCode,
			response.Header.Get("Location"),
		)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("successful OIDC callback omitted Cache-Control: no-store")
	}
	if !hasCookie(response.Cookies(), "session") {
		t.Fatal("successful OIDC callback omitted browser session cookie")
	}
	if !oidcTransactionCookieCleared(response.Cookies()) {
		t.Fatal("successful OIDC callback did not clear transaction cookie")
	}
}

func assertOIDCCallbackFailure(
	t *testing.T,
	response *http.Response,
	forbidden ...string,
) {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read OIDC callback failure: %v", err)
	}
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/login/oidc/failure" {
		t.Fatalf(
			"OIDC failure = %d %q, want 303 to query-free failure page",
			response.StatusCode,
			response.Header.Get("Location"),
		)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("OIDC callback failure omitted Cache-Control: no-store")
	}
	for _, value := range forbidden {
		if value != "" && (strings.Contains(string(body), value) ||
			strings.Contains(response.Header.Get("Location"), value)) {
			t.Fatalf("OIDC callback failure leaked %q", value)
		}
	}
	if hasCookie(response.Cookies(), "session") {
		t.Fatal("failed OIDC callback set a browser session cookie")
	}
	if !oidcTransactionCookieCleared(response.Cookies()) {
		t.Fatal("failed OIDC callback did not clear transaction cookie")
	}
}

func oidcTransactionCookieCleared(cookies []*http.Cookie) bool {
	for _, cookie := range cookies {
		if cookie.Name == oidc.TransactionCookieName && cookie.MaxAge < 0 {
			return true
		}
	}
	return false
}

func assertOIDCStateCounts(
	t *testing.T,
	h *authHarness,
	want [4]int,
) {
	t.Helper()
	if got := localLoginStateCounts(t, h); got != want {
		t.Fatalf("local OIDC state = %v, want %v", got, want)
	}
}

func alteredPKCECookie(
	t *testing.T,
	flow oidcCallbackFlow,
) *http.Cookie {
	t.Helper()
	transaction := readOIDCTransaction(
		t,
		flow.fixture.codec,
		flow.transactionCookie,
	)
	transaction.PKCEVerifier = strings.Repeat("b", sha256.Size+11)
	cookie, err := flow.fixture.codec.NewCookie(transaction)
	if err != nil {
		t.Fatalf("create altered PKCE transaction cookie: %v", err)
	}
	return cookie
}
