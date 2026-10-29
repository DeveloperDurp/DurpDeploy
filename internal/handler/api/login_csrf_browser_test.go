//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
	"durpdeploy/internal/secret"
)

type loginOriginObservation struct{ site, origin, host string }

func TestLoginCrossOriginBrowserE2E(t *testing.T) {
	f := newConfiguredBodyFixture(t, config.Defaults().Limits.HTTP)
	seedAPIUser(t, f.h.repo, "attacker@example.test", "viewer")
	observations := make(chan loginOriginObservation, 8)
	router := f.server.Config.Handler
	observed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/login" {
			observations <- loginOriginObservation{r.Header.Get("Sec-Fetch-Site"),
				r.Header.Get("Origin"), r.Host}
		}
		router.ServeHTTP(w, r)
	})
	upstream := httptest.NewServer(observed)
	t.Cleanup(upstream.Close)
	direct := httptest.NewTLSServer(observed)
	t.Cleanup(direct.Close)
	proxy := startBodyCaddy(t, upstream, direct.TLS.Certificates[0])
	attacker := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><title>Login CSRF fixture</title>
<form method="post" action="%s/login">
<input name="email" value="attacker@example.test">
<input type="password" name="password" value="testpass">
<button type="submit">Continue</button></form>`,
				html.EscapeString(r.URL.Query().Get("target")))
		}),
	)
	t.Cleanup(attacker.Close)
	attackerURL := strings.Replace(attacker.URL, "127.0.0.1", "localhost", 1)
	for mode, base := range map[string]string{"direct": direct.URL, "caddy": proxy} {
		for _, authenticated := range []bool{false, true} {
			t.Run(
				fmt.Sprintf("%s/victim=%t", mode, authenticated),
				func(t *testing.T) {
					checkLoginOriginBrowser(t, f, base, mode, attackerURL,
						authenticated, observations)
				},
			)
		}
	}
}

func checkLoginOriginBrowser(
	t *testing.T, f *artifactE2E, base, mode, attackerURL string,
	authenticated bool, observations <-chan loginOriginObservation,
) {
	t.Helper()
	b := startLivePackageBrowser(t, base)
	if authenticated {
		b.setBackTestSession(t, base, f.session)
	}
	before := loginOriginState(t, f)
	b.call(t, "Page.navigate", map[string]string{
		"url": attackerURL + "/?target=" + base,
	}, &struct{}{})
	b.wait(
		t,
		`document.querySelector('button') && document.readyState === 'complete'`,
	)
	b.evaluate(t, `document.querySelector('button').click(); true`)
	b.wait(t, fmt.Sprintf(`location.href === %q &&
document.body.textContent.trim() === 'Forbidden'`, base+"/login"))
	if string(b.evaluate(t,
		`performance.getEntriesByType('navigation')[0].responseStatus`,
	)) != "403" {
		t.Fatal("top-level cross-site login did not return 403")
	}
	checkLoginOriginObservation(
		t,
		observations,
		"cross-site",
		attackerURL,
		base,
	)
	if after := loginOriginState(t, f); after != before {
		t.Fatalf("cross-site form changed state: %v -> %v", before, after)
	}
	want := ""
	if authenticated {
		want = f.session
	}
	if loginBrowserSession(t, b, base) != want {
		t.Fatal("cross-site form created or replaced the browser session")
	}
	b.screenshot(t, fmt.Sprintf("login-csrf-%s-victim-%t", mode, authenticated))
	b.call(t, "Page.navigate", map[string]string{
		"url": base + "/settings/security",
	}, &struct{}{})
	if authenticated {
		b.wait(
			t,
			`location.pathname === '/settings/security' && document.querySelector('#app-navbar')`,
		)
	} else {
		b.wait(t, `location.pathname === '/login' && document.querySelector('#login-email')`)
	}
	checkLoginOriginBrowserMFA(t, f, b, base, mode, authenticated)
	checkLoginOriginObservation(t, observations, "same-origin", base, base)
}

func checkLoginOriginObservation(
	t *testing.T, observations <-chan loginOriginObservation,
	site, origin, base string,
) {
	t.Helper()
	select {
	case seen := <-observations:
		if seen.site != site || seen.origin != origin ||
			"https://"+seen.host != base {
			t.Fatalf("browser metadata = %+v", seen)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s form did not reach the real login route", site)
	}
}

func loginBrowserSession(t *testing.T, b *packageBrowser, base string) string {
	t.Helper()
	var cookies struct {
		Cookies []struct{ Name, Value string }
	}
	b.call(
		t,
		"Network.getCookies",
		map[string]any{"urls": []string{base}},
		&cookies,
	)
	for _, cookie := range cookies.Cookies {
		if cookie.Name == "session" {
			return cookie.Value
		}
	}
	return ""
}

func checkLoginOriginBrowserMFA(
	t *testing.T, f *artifactE2E, b *packageBrowser,
	base, mode string, authenticated bool,
) {
	t.Helper()
	user := seedAPIUser(t, f.h.repo,
		fmt.Sprintf("mfa-%s-%t@example.test", mode, authenticated), "viewer")
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	const seed = "JBSWY3DPEHPK3PXP"
	encrypted, err := box.Encrypt(seed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateTOTP(t.Context(), db.CreateTOTPParams{
		UserID: user.ID, EncryptedSeed: []byte(encrypted),
	}); err != nil {
		t.Fatal(err)
	}
	b.navigateBackTest(t, base+"/login")
	b.evaluate(t, fmt.Sprintf(`(() => {
const form = document.querySelector('form[action="/login"]');
form.elements.email.value = %q; form.elements.password.value = 'testpass';
form.requestSubmit(); return true;
})()`, user.Email))
	b.wait(
		t,
		`location.pathname === '/login/mfa' && document.querySelector('#totp-code')`,
	)
	code, err := mfa.GenerateTOTPCode(seed, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b.evaluate(t, fmt.Sprintf(`(() => {
const input = document.querySelector('#totp-code'); input.value = %q;
input.form.requestSubmit(); return true;
})()`, code))
	b.wait(
		t,
		`location.pathname === '/' && document.querySelector('#app-navbar')`,
	)
	session, err := f.h.repo.Queries.GetSession(
		t.Context(),
		db.GetSessionParams{
			ID: loginBrowserSession(t, b, base), ExpiresAt: 0,
		},
	)
	if err != nil || session.UserID != user.ID {
		t.Fatal("same-origin MFA login failed to issue the correct identity")
	}
	b.screenshot(t, fmt.Sprintf("login-csrf-%s-mfa-%t", mode, authenticated))
}
