//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/oidc"
	"durpdeploy/internal/oidc/oidctest"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

func TestOIDCNavigationBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	// A different-origin callback records whether SSO used a document request.
	destination := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Sec-Fetch-Mode") != "navigate" ||
				r.Header.Get("HX-Request") != "" {
				http.Error(w, "expected native navigation", 400)
				return
			}
			_, _ = w.Write([]byte("SSO redirect reached"))
		}),
	)
	t.Cleanup(destination.Close)
	issuer, err := oidctest.New(oidctest.Options{CallbackURL: destination.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(issuer.Close)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	codec, err := oidc.NewTransactionCookieCodec(
		box,
		oidc.TransactionCookieConfig{Now: issuer.Now},
	)
	if err != nil {
		t.Fatal(err)
	}
	transactions, err := oidc.NewTransactionStore(
		oidc.TransactionStoreOptions{Repository: f.h.repo, CookieCodec: codec},
	)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := oidc.NewProvider(oidc.ProviderOptions{
		Config: oidc.Config{
			Enabled:      true,
			Issuer:       issuer.URL(),
			ClientID:     issuer.ClientID(),
			ClientSecret: issuer.ClientSecret(),
			CallbackURL:  destination.URL,
			Scopes:       []string{"openid"},
		},
		HTTPClient: issuer.Client(), Now: issuer.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	user, err := f.h.repo.Queries.GetUserByEmail(
		t.Context(),
		"artifact-e2e@example.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateOIDCIdentity(t.Context(), db.CreateOIDCIdentityParams{Issuer: issuer.URL(), Subject: "fixture-subject", UserID: user.ID}); err != nil {
		t.Fatal(err)
	}
	authHandler := handler.NewAuthHandler(f.h.repo)
	authHandler.SetOIDCLogin(provider, transactions)
	app := httptest.NewServer(server.NewRouter(
		f.h.repo,
		f.h.runner,
		cron.NewParser(
			cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow,
		),
		authHandler,
		true,
	))
	t.Cleanup(app.Close)
	request, err := http.NewRequestWithContext(
		t.Context(),
		"GET",
		app.URL+"/settings/security/reauth/oidc",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: "session", Value: f.session})
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 303 ||
		!strings.HasPrefix(
			response.Header.Get("Location"),
			issuer.URL()+"/authorize?",
		) {
		t.Fatalf(
			"OIDC start: %d %s",
			response.StatusCode,
			response.Header.Get("Location"),
		)
	}
	b := startPackageBrowser(t)
	// The only ignored certificate is the process-local fixture's browser TLS.
	b.call(
		t,
		"Security.setIgnoreCertificateErrors",
		map[string]bool{"ignore": true},
		&struct{}{},
	)
	b.setBackTestSession(t, app.URL, f.session)
	b.navigateBackTest(t, app.URL+"/settings/security/reauth")
	b.wait(
		t,
		`window.Alpine && document.querySelector('a[href="/projects"]').getAttribute('hx-boost') === 'true'`,
	)
	if string(
		b.evaluate(
			t,
			`document.querySelector('a[href="/settings/security/reauth/oidc"]').getAttribute('hx-boost') === 'true'`,
		),
	) == "true" {
		t.Fatal("OIDC redirect link was boosted")
	}
	b.captureNavigation(t, "oidc-reauth")
	b.evaluate(
		t,
		`document.querySelector('a[href="/settings/security/reauth/oidc"]').click(); true`,
	)
	b.wait(
		t,
		fmt.Sprintf(
			`location.origin === %q && document.body.textContent === 'SSO redirect reached'`,
			destination.URL,
		),
	)
	if issuer.Capture().Authorization.Prompt != "login" ||
		issuer.Capture().Authorization.MaxAge != "0" {
		t.Fatal("SSO did not request fresh authentication")
	}
}
