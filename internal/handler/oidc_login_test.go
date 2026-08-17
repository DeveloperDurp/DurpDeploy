package handler_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/oidc"
	"durpdeploy/internal/oidc/oidctest"
	"durpdeploy/internal/secret"
)

func TestOIDCLoginStart_RedirectsWithBoundTransaction(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	fixture := configureOIDCLoginStart(t, h)
	loginServer := startOIDCLoginServer(t, h)
	before := localLoginStateCounts(t, h)

	// When
	response := getOIDCLoginStart(t, loginServer.URL)
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", response.StatusCode)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	cookie := oidcTransactionCookie(t, response.Cookies())
	if !cookie.HttpOnly || !cookie.Secure ||
		cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/login/oidc" {
		t.Fatalf("OIDC cookie has unsafe attributes: %#v", cookie)
	}
	transaction := readOIDCTransaction(t, fixture.codec, cookie)
	if transaction.Mode != oidc.TransactionModeLogin ||
		transaction.Reauth != (oidc.ReauthBinding{}) {
		t.Fatalf("transaction = %#v, want login transaction", transaction)
	}
	if got, want := response.Header.Get("Location"),
		expectedAuthorizationURL(fixture.provider, transaction); got != want {
		t.Fatalf("redirect URL = %q, want %q", got, want)
	}
	if got, want := localLoginStateCounts(t, h),
		[4]int{before[0], before[1], before[2], before[3] + 1}; got != want {
		t.Fatalf("local state = %v, want %v", got, want)
	}
	if hasCookie(response.Cookies(), "session") {
		t.Fatal("OIDC login start created a browser session")
	}
}

func TestOIDCLoginStart_RendersGenericLogin_whenDiscoveryUnavailable(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	fixture := configureOIDCLoginStart(t, h)
	fixture.provider.SetDiscoveryFailures(1)
	loginServer := startOIDCLoginServer(t, h)

	// When
	response := getOIDCLoginStart(t, loginServer.URL)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read outage response: %v", err)
	}

	// Then
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.StatusCode)
	}
	page := string(body)
	if !strings.Contains(page, "Single sign-on is temporarily unavailable") {
		t.Fatalf("outage page omitted generic error: %q", page)
	}
	if strings.Contains(page, fixture.provider.URL()) {
		t.Fatal("outage page leaked provider detail")
	}
	if !strings.Contains(page, `name="password"`) {
		t.Fatal("outage page omitted password fallback form")
	}
	if hasCookie(response.Cookies(), oidc.TransactionCookieName) ||
		hasCookie(response.Cookies(), "session") {
		t.Fatal("outage response set a browser credential")
	}
}

func TestOIDCLoginStart_AllowsPasswordFallbackAfterOutage(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	user := h.seedUser(t, "fallback@example.test", "hunter2")
	fixture := configureOIDCLoginStart(t, h)
	fixture.provider.SetDiscoveryFailures(1)
	loginServer := startOIDCLoginServer(t, h)
	client := newJar(t)

	response := getOIDCLoginStart(t, loginServer.URL)
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("OIDC outage status = %d, want 503", response.StatusCode)
	}

	// When
	passwordResponse, err := client.PostForm(h.server+"/login", url.Values{
		"email":    {user.Email},
		"password": {"hunter2"},
	})
	if err != nil {
		t.Fatalf("post password fallback: %v", err)
	}
	defer passwordResponse.Body.Close()

	// Then
	if passwordResponse.StatusCode != http.StatusSeeOther ||
		passwordResponse.Header.Get("Location") != "/" {
		t.Fatalf(
			"password fallback = %d %q, want 303 to /",
			passwordResponse.StatusCode,
			passwordResponse.Header.Get("Location"),
		)
	}
	if !hasCookie(passwordResponse.Cookies(), "session") {
		t.Fatal("password fallback omitted browser session")
	}
	if got := sessionCount(t, h, user.ID); got != 1 {
		t.Fatalf("password fallback session count = %d, want 1", got)
	}
}

type oidcLoginStartFixture struct {
	provider *oidctest.Fixture
	codec    *oidc.TransactionCookieCodec
}

func configureOIDCLoginStart(
	t *testing.T,
	h *authHarness,
) oidcLoginStartFixture {
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
		oidc.TransactionCookieConfig{Secure: true, Now: providerFixture.Now},
	)
	if err != nil {
		t.Fatalf("new transaction cookie codec: %v", err)
	}
	transactions, err := oidc.NewTransactionStore(oidc.TransactionStoreOptions{
		Repository: h.repo, CookieCodec: codec,
	})
	if err != nil {
		t.Fatalf("new transaction store: %v", err)
	}
	provider, err := oidc.NewProvider(oidc.ProviderOptions{
		Config: oidc.Config{
			Enabled:      true,
			Issuer:       providerFixture.URL(),
			ClientID:     providerFixture.ClientID(),
			ClientSecret: providerFixture.ClientSecret(),
			CallbackURL:  "https://deploy.example/login/oidc/callback",
			Scopes:       []string{"openid", "profile", "email"},
		},
		HTTPClient: providerFixture.Client(),
		Now:        providerFixture.Now,
	})
	if err != nil {
		t.Fatalf("new OIDC provider: %v", err)
	}
	h.authHandler.SetOIDCLogin(provider, transactions)
	return oidcLoginStartFixture{provider: providerFixture, codec: codec}
}

func startOIDCLoginServer(t *testing.T, h *authHarness) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(h.authHandler.LoginOIDCGet))
	t.Cleanup(server.Close)
	return server
}

func getOIDCLoginStart(t *testing.T, serverURL string) *http.Response {
	t.Helper()
	client := newJar(t)
	response, err := client.Get(
		serverURL + "/login/oidc?return_to=https://bad.test",
	)
	if err != nil {
		t.Fatalf("start OIDC login: %v", err)
	}
	return response
}

func oidcTransactionCookie(t *testing.T, cookies []*http.Cookie) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == oidc.TransactionCookieName {
			return cookie
		}
	}
	t.Fatal("OIDC login start omitted transaction cookie")
	return nil
}

func readOIDCTransaction(
	t *testing.T,
	codec *oidc.TransactionCookieCodec,
	cookie *http.Cookie,
) oidc.Transaction {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodGet,
		"https://deploy.example/login/oidc/callback",
		nil,
	)
	request.AddCookie(cookie)
	transaction, err := codec.ReadCookie(request)
	if err != nil {
		t.Fatalf("read OIDC transaction cookie: %v", err)
	}
	return transaction
}

func expectedAuthorizationURL(
	provider *oidctest.Fixture,
	transaction oidc.Transaction,
) string {
	challenge := sha256.Sum256([]byte(transaction.PKCEVerifier))
	query := url.Values{}
	query.Set("client_id", provider.ClientID())
	query.Set(
		"code_challenge",
		base64.RawURLEncoding.EncodeToString(challenge[:]),
	)
	query.Set("code_challenge_method", "S256")
	query.Set("nonce", transaction.Nonce)
	query.Set("redirect_uri", "https://deploy.example/login/oidc/callback")
	query.Set("response_type", "code")
	query.Set("scope", "openid profile email")
	query.Set("state", transaction.State)
	return provider.URL() + "/authorize?" + query.Encode()
}

func localLoginStateCounts(t *testing.T, h *authHarness) (counts [4]int) {
	t.Helper()
	if err := h.repo.DB.QueryRowContext(context.Background(), `
		SELECT
			(SELECT COUNT(*) FROM users),
			(SELECT COUNT(*) FROM sessions),
			(SELECT COUNT(*) FROM audit_log),
			(SELECT COUNT(*) FROM oidc_transactions)`,
	).Scan(&counts[0], &counts[1], &counts[2], &counts[3]); err != nil {
		t.Fatalf("count local login state: %v", err)
	}
	return counts
}

func hasCookie(cookies []*http.Cookie, name string) bool {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return true
		}
	}
	return false
}
