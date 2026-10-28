//go:build e2e && transportsecurity

package api_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type transportFixture struct {
	URL         string `json:"url"`
	HTTPURL     string `json:"http_url"`
	CAFile      string `json:"ca_file"`
	AgentURL    string `json:"agent_url"`
	AgentCA     string `json:"agent_ca"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	Token       string `json:"token"`
	ForwardURL  string `json:"forward_url"`
	ExpectedIP  string `json:"expected_ip"`
	ForwardMode string `json:"forward_mode"`
}

func loadTransportFixture(t *testing.T) transportFixture {
	t.Helper()
	data, err := os.ReadFile(os.Getenv("HTTPS_SECURITY_FIXTURE"))
	if err != nil {
		t.Fatal("private transport fixture is required")
	}
	var f transportFixture
	if json.Unmarshal(data, &f) != nil || f.Token == "" ||
		f.Password == "" || f.HTTPURL == "" {
		t.Fatal("incomplete transport fixture")
	}
	return f
}

func (f transportFixture) client(t *testing.T) *http.Client {
	t.Helper()
	ca, err := os.ReadFile(f.CAFile)
	if err != nil {
		t.Fatal("fixture CA missing")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid fixture CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: roots, MinVersion: tls.VersionTLS12,
	}}
	base, err := url.Parse(f.URL)
	if err != nil {
		t.Fatal("invalid fixture origin")
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || host != base.Hostname() {
			return nil, os.ErrPermission
		}
		dialer := net.Dialer{Timeout: 3 * time.Second}
		return dialer.DialContext(
			ctx,
			network,
			net.JoinHostPort("127.0.0.1", port),
		)
	}
	t.Cleanup(transport.CloseIdleConnections)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Transport: transport, Jar: jar, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func transportResponse(
	t *testing.T,
	client *http.Client,
	req *http.Request,
	status int,
) (*http.Response, []byte) {
	t.Helper()
	res, err := client.Do(req)
	if err != nil {
		t.Fatal("fixture request failed")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil || res.StatusCode != status {
		t.Fatalf("expected HTTP %d, received %d", status, res.StatusCode)
	}
	return res, body
}

func transportRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method,
		target, strings.NewReader(body))
	if err != nil {
		t.Fatal("invalid fixture request")
	}
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return req
}

func transportLogin(
	t *testing.T,
	f transportFixture,
	client *http.Client,
) *http.Response {
	t.Helper()
	form := url.Values{"email": {f.Email}, "password": {f.Password}}
	req := transportRequest(t, "POST", f.URL+"/login", form.Encode())
	// Scheme and host headers cannot override the configured public origin.
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("X-Forwarded-Host", "untrusted.invalid")
	req.Header.Set("X-Forwarded-For", "203.0.113.90")
	req.Header.Set("Forwarded", "for=203.0.113.91;proto=http")
	res, _ := transportResponse(t, client, req, 303)
	return res
}

func TestTransportCookiesAPIWebE2E(t *testing.T) {
	f := loadTransportFixture(t)
	client := f.client(t)
	res := transportLogin(t, f, client)
	found := false
	for _, cookie := range res.Cookies() {
		if cookie.Name != "session" {
			continue
		}
		found = true
		if !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" ||
			cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode ||
			!cookie.Expires.After(time.Now()) {
			t.Fatal("session cookie violates HTTPS policy")
		}
	}
	if !found {
		t.Fatal("login did not issue a final session")
	}
	_, body := transportResponse(t, client,
		transportRequest(t, "GET", f.URL+"/", ""), 200)
	marker := `<meta name="csrf-token" content="`
	_, rest, ok := strings.Cut(string(body), marker)
	if !ok {
		t.Fatal("authenticated page lacks CSRF metadata")
	}
	csrf, _, _ := strings.Cut(rest, `"`)
	transportResponse(t, client,
		transportRequest(t, "POST", f.URL+"/projects", "name=forbidden"),
		403)
	res, _ = transportResponse(t, client, transportRequest(t, "POST",
		f.URL+"/logout", url.Values{"csrf_token": {csrf}}.Encode()), 303)
	deleted := false
	for _, cookie := range res.Cookies() {
		if cookie.Name == "session" && cookie.MaxAge < 0 && cookie.Secure &&
			cookie.HttpOnly && cookie.SameSite == http.SameSiteLaxMode {
			deleted = true
		}
	}
	if !deleted {
		t.Fatal("logout did not securely delete the cookie")
	}
	transportResponse(t, client,
		transportRequest(t, "GET", f.URL+"/", ""), 303)
	checkTransportLoginAudit(t, f)
}

func checkTransportLoginAudit(t *testing.T, f transportFixture) {
	t.Helper()
	req := transportRequest(t, "GET",
		f.URL+"/api/v1/admin/audit?action=login", "")
	req.Header.Set("Authorization", "Bearer "+f.Token)
	_, body := transportResponse(t, f.client(t), req, 200)
	var result struct {
		Items []struct{ Details sql.NullString }
	}
	if json.Unmarshal(body, &result) != nil || len(result.Items) == 0 {
		t.Fatal("public audit API did not return login evidence")
	}
	for _, item := range result.Items {
		var details struct {
			IP string `json:"ip"`
		}
		if !item.Details.Valid ||
			json.Unmarshal([]byte(item.Details.String), &details) != nil ||
			details.IP == "" || strings.HasPrefix(details.IP, "203.0.113.") {
			t.Fatal("forwarded input replaced canonical audit identity")
		}
	}
}

func TestTransportHeadersRedirectsAPIE2E(t *testing.T) {
	f := loadTransportFixture(t)
	for _, path := range []string{
		"/login", "/api/v1/healthz", "/api/v1/projects", "/missing",
		"/debug/pprof/", "/metrics", "/durpdeploy.db", "/.env",
	} {
		t.Run(path, func(t *testing.T) {
			status := 404
			switch path {
			case "/login", "/api/v1/healthz":
				status = 200
			case "/api/v1/projects":
				status = 401
			}
			res, _ := transportResponse(t, f.client(t),
				transportRequest(t, "GET", f.URL+path, ""), status)
			if res.Header.Get("Strict-Transport-Security") != "max-age=86400" ||
				res.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing HTTPS response policy")
			}
		})
	}
	res, _ := transportResponse(t, f.client(t), transportRequest(t,
		"GET", f.HTTPURL+"/login?next=%2Fprojects&source=transport", ""), 308)
	location, err := url.Parse(res.Header.Get("Location"))
	base, _ := url.Parse(f.URL)
	if err != nil || location.Scheme != "https" ||
		location.Hostname() != base.Hostname() || location.Path != "/login" ||
		location.RawQuery != "next=%2Fprojects&source=transport" {
		t.Fatal("HTTP redirect lost its HTTPS origin, path, or query")
	}
	if res.Header.Get("Strict-Transport-Security") != "" ||
		len(res.Cookies()) != 0 {
		t.Fatal("HTTP redirect must not issue cookies or HSTS")
	}
}

func TestTransportForwardedRateLimitE2E(t *testing.T) {
	f := loadTransportFixture(t)
	client := f.client(t)
	for i := 0; i < 21; i++ {
		req := transportRequest(t, "POST", f.URL+"/login/mfa/totp", "")
		req.Header.Set("X-Forwarded-For", "203.0.113."+strconv.Itoa(i+10))
		req.Header.Set("X-Real-IP", "198.51.100.1")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal("bounded rate-limit request failed")
		}
		res.Body.Close()
		if (i < 20 && res.StatusCode == 429) ||
			(i == 20 && (res.StatusCode != 429 ||
				res.Header.Get("Retry-After") != "900")) {
			t.Fatal("spoofed forwarding headers changed the rate-limit budget")
		}
	}
}
