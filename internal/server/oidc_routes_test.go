package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/oidc"
	"durpdeploy/internal/oidc/oidctest"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"

	"github.com/robfig/cron/v3"
)

type oidcRouterHarness struct {
	repo        *repository.Repository
	authHandler *handler.AuthHandler
	runner      *runner.DeploymentRunner
	parser      cron.Parser
}

func newOIDCRouterHarness(t *testing.T) *oidcRouterHarness {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		filepath.Join(t.TempDir(), "router.db"),
	)
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate router database: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close router database: %v", err)
		}
	})

	repo := repository.New(conn)
	return &oidcRouterHarness{
		repo:        repo,
		authHandler: handler.NewAuthHandler(repo),
		runner:      runner.New(repo, runner.NewLogBroker()),
		parser: cron.NewParser(
			cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
		),
	}
}

func (h *oidcRouterHarness) server(
	t *testing.T,
	oidcEnabled bool,
) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(NewRouter(
		h.repo,
		h.runner,
		h.parser,
		h.authHandler,
		oidcEnabled,
	))
	t.Cleanup(server.Close)
	return server
}

func (h *oidcRouterHarness) configureOIDC(
	t *testing.T,
) *oidctest.Fixture {
	t.Helper()
	fixture, err := oidctest.New(oidctest.Options{})
	if err != nil {
		t.Fatalf("new OIDC fixture: %v", err)
	}
	t.Cleanup(fixture.Close)

	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new transaction secret box: %v", err)
	}
	codec, err := oidc.NewTransactionCookieCodec(
		box,
		oidc.TransactionCookieConfig{Secure: true, Now: fixture.Now},
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
			Enabled:       true,
			Issuer:        fixture.URL(),
			ClientID:      fixture.ClientID(),
			ClientSecret:  fixture.ClientSecret(),
			CallbackURL:   "https://deploy.example/login/oidc/callback",
			Scopes:        []string{"openid", "profile", "email"},
			GroupClaim:    "groups",
			AdminGroup:    "fixture-admin",
			DeployerGroup: "fixture-deployer",
			ViewerGroup:   "fixture-viewer",
		},
		HTTPClient: fixture.Client(),
		Now:        fixture.Now,
	})
	if err != nil {
		t.Fatalf("new OIDC provider: %v", err)
	}
	h.authHandler.SetOIDCDisplayName("Fixture SSO")
	h.authHandler.SetOIDCLogin(provider, transactions)
	return fixture
}

func oidcRouterClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("new cookie jar: %v", err)
	}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func TestOIDCRoutes_AreAbsent_when_disabled(t *testing.T) {
	// Given
	harness := newOIDCRouterHarness(t)
	app := harness.server(t, false)
	client := oidcRouterClient(t)

	// When
	loginResponse, err := client.Get(app.URL + "/login")
	if err != nil {
		t.Fatalf("get login page: %v", err)
	}
	defer loginResponse.Body.Close()
	loginPage, err := io.ReadAll(loginResponse.Body)
	if err != nil {
		t.Fatalf("read login page: %v", err)
	}

	// Then
	if loginResponse.StatusCode != http.StatusOK {
		t.Fatalf("login page status = %d, want 200", loginResponse.StatusCode)
	}
	if strings.Contains(string(loginPage), `href="/login/oidc"`) {
		t.Fatal("disabled login page exposed OIDC action")
	}
	for _, path := range []string{
		"/login/oidc",
		"/login/oidc/callback",
		"/login/oidc/failure",
		"/settings/security/reauth/oidc",
	} {
		t.Run(path, func(t *testing.T) {
			response, err := client.Get(app.URL + path)
			if err != nil {
				t.Fatalf("get %s: %v", path, err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("%s status = %d, want 404", path, response.StatusCode)
			}
		})
	}
}

func TestOIDCRoutes_ExposePublicStartAndCallback_when_enabled(t *testing.T) {
	// Given
	harness := newOIDCRouterHarness(t)
	harness.configureOIDC(t)
	app := harness.server(t, true)
	client := oidcRouterClient(t)

	// When
	startResponse, err := client.Get(app.URL + "/login/oidc")
	if err != nil {
		t.Fatalf("start OIDC login: %v", err)
	}
	defer startResponse.Body.Close()
	callbackResponse, err := client.Get(
		app.URL + "/login/oidc/callback?state=missing",
	)
	if err != nil {
		t.Fatalf("call OIDC callback: %v", err)
	}
	defer callbackResponse.Body.Close()
	failureResponse, err := client.Get(app.URL + "/login/oidc/failure")
	if err != nil {
		t.Fatalf("get OIDC failure page: %v", err)
	}
	defer failureResponse.Body.Close()
	failurePage, err := io.ReadAll(failureResponse.Body)
	if err != nil {
		t.Fatalf("read OIDC failure page: %v", err)
	}
	reauthResponse, err := client.Get(
		app.URL + "/settings/security/reauth/oidc",
	)
	if err != nil {
		t.Fatalf("start OIDC reauthentication: %v", err)
	}
	defer reauthResponse.Body.Close()

	// Then
	if startResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("OIDC start status = %d, want 303", startResponse.StatusCode)
	}
	if callbackResponse.StatusCode != http.StatusSeeOther ||
		callbackResponse.Header.Get("Location") != "/login/oidc/failure" {
		t.Fatalf(
			"OIDC callback = %d %q, want 303 to query-free failure page",
			callbackResponse.StatusCode,
			callbackResponse.Header.Get("Location"),
		)
	}
	if failureResponse.StatusCode != http.StatusUnprocessableEntity ||
		failureResponse.Header.Get("Cache-Control") != "no-store" ||
		!strings.Contains(
			string(failurePage),
			"Single sign-on could not be completed",
		) {
		t.Fatalf(
			"OIDC failure page = %d %q %q, want generic no-store 422 page",
			failureResponse.StatusCode,
			failureResponse.Header.Get("Cache-Control"),
			failurePage,
		)
	}
	if reauthResponse.StatusCode != http.StatusSeeOther ||
		reauthResponse.Header.Get("Location") != "/login" {
		t.Fatalf(
			"anonymous OIDC reauth = %d %q, want 303 to /login",
			reauthResponse.StatusCode,
			reauthResponse.Header.Get("Location"),
		)
	}
}

func (h *oidcRouterHarness) createUser(
	t *testing.T,
	email string,
	passwordHash string,
) int64 {
	t.Helper()
	user, err := h.repo.Queries.CreateUser(
		context.Background(),
		db.CreateUserParams{
			Email: email, PasswordHash: passwordHash, Name: "OIDC Router User", Role: "admin",
		},
	)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user.ID
}
