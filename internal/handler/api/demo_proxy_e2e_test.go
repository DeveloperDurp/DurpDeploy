//go:build e2e

package api_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestDemoProxyHeadersAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	tlsServer := httptest.NewTLSServer(f.server.Config.Handler)
	t.Cleanup(tlsServer.Close)
	proxy := startBodyCaddy(t, f.server, tlsServer.TLS.Certificates[0], true)
	public, err := url.Parse(proxy)
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []int{1, 2} {
		client := bodyProxyClient(t, tlsServer.Certificate(), protocol)
		for _, path := range []string{"/login", "/projects", "/api/v1/projects"} {
			req, err := http.NewRequestWithContext(
				t.Context(),
				"GET",
				proxy+path,
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			switch path {
			case "/projects":
				req.AddCookie(&http.Cookie{Name: "session", Value: f.session})
			case "/api/v1/projects":
				req.Header.Set("Authorization", "Bearer "+f.token)
			}
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			checkDemoProxyResponse(t, res, path, protocol, public.Port())
		}
	}
}

func checkDemoProxyResponse(
	t *testing.T,
	res *http.Response,
	path string,
	protocol int,
	port string,
) {
	t.Helper()
	if res.StatusCode != http.StatusOK || res.ProtoMajor != protocol {
		t.Fatalf(
			"%s: status=%d protocol=%d",
			path,
			res.StatusCode,
			res.ProtoMajor,
		)
	}
	got := res.Header.Values("Strict-Transport-Security")
	if len(got) != 1 || got[0] != "max-age=86400" {
		t.Fatalf("%s: HSTS=%q", path, got)
	}
	want := `h3=":` + port + `"; ma=86400`
	if got := res.Header.Values("Alt-Svc"); len(got) != 1 || got[0] != want {
		t.Fatalf("%s: Alt-Svc=%q, want %q", path, got, want)
	}
}
