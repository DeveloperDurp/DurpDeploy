//go:build e2e && transportsecurity && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestTransportBrowserE2E(t *testing.T) {
	var f struct{ URL, Email, Password string }
	data, err := os.ReadFile(os.Getenv("HTTPS_SECURITY_FIXTURE"))
	if err != nil || json.Unmarshal(data, &f) != nil ||
		f.URL == "" || f.Email == "" || f.Password == "" ||
		os.Getenv("DURPDEPLOY_TEST_BROWSER_ENDPOINT") == "" {
		t.Fatal("owned isolated browser fixture is required")
	}
	fmt.Println("TRANSPORT_BROWSER_STEP cdp-connect")
	b := startPackageBrowser(t)
	fmt.Println("TRANSPORT_BROWSER_STEP login-page")
	b.call(
		t,
		"Page.navigate",
		map[string]string{"url": f.URL + "/login"},
		&struct{}{},
	)
	b.wait(
		t,
		`document.querySelector('#login-email') && document.readyState==='complete'`,
	)
	fmt.Println("TRANSPORT_BROWSER_STEP login-submit")
	email, _ := json.Marshal(f.Email)
	password, _ := json.Marshal(f.Password)
	b.evaluate(t, fmt.Sprintf(`(() => {
 const form=document.querySelector('form[action="/login"]');
 form.elements.email.value=%s; form.elements.password.value=%s;
 form.requestSubmit(); return true;
})()`, email, password))
	b.wait(
		t,
		`location.pathname==='/' && document.querySelector('#app-navbar')`,
	)
	fmt.Println("TRANSPORT_BROWSER_STEP session-cookie")
	b.evaluate(t, `(() => {
 if(location.protocol!=='https:' || document.cookie.includes('session='))
   throw new Error('browser session transport contract failed');
 return true;
})()`)
	var cookies struct {
		Cookies []struct {
			Name, SameSite   string
			Secure, HTTPOnly bool
		}
	}
	b.call(
		t,
		"Network.getCookies",
		map[string]any{"urls": []string{f.URL}},
		&cookies,
	)
	found := false
	for _, c := range cookies.Cookies {
		if c.Name == "session" {
			found = c.Secure && c.HTTPOnly && c.SameSite == "Lax"
		}
	}
	if !found {
		t.Fatal("browser did not retain the protected session cookie")
	}
	fmt.Println("TRANSPORT_BROWSER_STEP authenticated-screenshot")
	b.screenshot(t, "transport-authenticated-https")
	fmt.Println("TRANSPORT_BROWSER_STEP logout-submit")
	b.evaluate(t, `(() => {
 const form=document.createElement('form'); form.method='POST'; form.action='/logout';
 const input=document.createElement('input'); input.name='csrf_token';
 input.value=document.querySelector('meta[name="csrf-token"]').content;
 form.append(input); document.body.append(form); form.requestSubmit(); return true;
})()`)
	fmt.Println("TRANSPORT_BROWSER_STEP logout-page")
	b.wait(
		t,
		`location.pathname==='/login' && document.querySelector('#login-email')`,
	)
	fmt.Println("TRANSPORT_BROWSER_STEP logout-screenshot")
	b.screenshot(t, "transport-logout")
}
