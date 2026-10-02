package artifact

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProductionClientEnforcesRedirectBoundary(t *testing.T) {
	for _, test := range []struct {
		name    string
		allowed bool
	}{
		{"same-origin", true}, {"cross-origin", false},
		{"redirect-loop", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given: real TLS endpoints and the production transport and dialer.
			var leaked, badCredential atomic.Bool
			destination, _ := artifactTLSServer(
				t,
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					leaked.Store(true)
					w.WriteHeader(http.StatusNoContent)
				}),
			)
			data := zipFixture(t, "package.txt", 0644)
			var requests atomic.Int32
			server, client := artifactTLSServer(
				t,
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Header.Get(
						"Authorization",
					) != "Bearer test-credential" {
						badCredential.Store(true)
					}
					if r.URL.Path == "/final.zip" {
						if _, err := w.Write(data); err != nil {
							t.Error(err)
						}
						return
					}
					target := "/final.zip"
					switch test.name {
					case "cross-origin":
						target = destination.URL + "/final.zip"
					case "redirect-loop":
						target = "/1.zip"
					}
					http.Redirect(w, r, target, http.StatusFound)
				}),
			)
			client.HTTP.Transport.(*http.Transport).TLSClientConfig.RootCAs.AddCert(
				destination.Certificate(),
			)
			if test.name == "cross-origin" {
				probe, err := client.HTTP.Get(destination.URL)
				if err != nil {
					t.Fatalf(
						"trusted redirect destination is unreachable: %v",
						err,
					)
				}
				probe.Body.Close()
				leaked.Store(false)
			}
			// When: fetching through the redirect.
			download, err := client.Fetch(t.Context(), Repository{
				URLTemplate: server.URL + "/{version}.zip",
				AuthType:    "bearer",
				Credential:  "test-credential",
			}, Pin{URL: server.URL + "/1.zip"})
			if download.Path != "" {
				defer os.Remove(download.Path)
			}
			// Then: only bounded same-origin HTTPS redirects receive credentials.
			if test.allowed {
				if err != nil || download.Size != int64(len(data)) ||
					requests.Load() != 2 {
					t.Fatalf("same-origin fetch failed: %v", err)
				}
			} else if !errors.Is(err, ErrFetch) {
				t.Fatalf("unsafe redirect accepted: %v", err)
			}
			if leaked.Load() || badCredential.Load() {
				t.Fatal("redirect credential boundary was violated")
			}
			if test.name == "redirect-loop" && requests.Load() != 5 {
				t.Fatalf("redirect limit was not enforced: %d", requests.Load())
			}
		})
	}
}

func TestProductionClientRejectsHTTPSDowngradeOnSameAuthority(t *testing.T) {
	// Given: the exact same host/port can receive TLS and plaintext HTTP.
	var plaintext atomic.Bool
	var downgradeTarget atomic.Pointer[string]
	server, client := artifactTLSServer(
		t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, *downgradeTarget.Load(), http.StatusFound)
		}),
		&plaintext,
	)
	target := strings.Replace(
		server.URL,
		"https://",
		"http://",
		1,
	) + "/final.zip"
	downgradeTarget.Store(&target)
	probe, err := http.Get(
		strings.Replace(server.URL, "https://", "http://", 1),
	)
	if err != nil {
		t.Fatalf("plaintext fixture is unreachable: %v", err)
	}
	probe.Body.Close()
	if !plaintext.Load() {
		t.Fatal("fixture did not observe plaintext HTTP")
	}
	plaintext.Store(false)
	// When: fetching a redirect to the reachable HTTP destination.
	_, err = client.Fetch(t.Context(), Repository{
		URLTemplate: server.URL + "/{version}.zip",
		AuthType:    "bearer",
		Credential:  "test-credential",
	}, Pin{URL: server.URL + "/1.zip"})
	// Then: the boundary rejects it before any plaintext request is sent.
	if !errors.Is(err, ErrFetch) || plaintext.Load() {
		t.Fatalf("HTTPS downgrade reached plaintext endpoint: %v", err)
	}
}

func TestProductionClientIgnoresEnvironmentProxies(t *testing.T) {
	// ProxyFromEnvironment caches its first environment read. A fresh process
	// makes this regression independent of other HTTP tests and shuffle order.
	if os.Getenv("DURPDEPLOY_PROXY_TEST_CHILD") != "1" {
		command := exec.CommandContext(
			t.Context(),
			os.Args[0],
			"-test.run=^TestProductionClientIgnoresEnvironmentProxies$",
		)
		command.Env = append(os.Environ(), "DURPDEPLOY_PROXY_TEST_CHILD=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("proxy boundary subprocess failed: %v\n%s", err, output)
		}
		return
	}
	// Given: hostile proxy settings and a permitted private HTTPS repository.
	var reachedProxy atomic.Bool
	proxy := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reachedProxy.Store(true)
			w.WriteHeader(http.StatusForbidden)
		}),
	)
	defer proxy.Close()
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(name, proxy.URL)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	data := zipFixture(t, "package.txt", 0644)
	server, client := artifactTLSServer(
		t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := w.Write(data); err != nil {
				t.Error(err)
			}
		}),
	)
	// When: the production client fetches the package.
	download, err := client.Fetch(t.Context(), Repository{
		URLTemplate: server.URL + "/{version}.zip", AuthType: "noauth",
	}, Pin{URL: server.URL + "/1.zip"})
	if download.Path != "" {
		defer os.Remove(download.Path)
	}
	// Then: the direct connection succeeds without contacting the proxy.
	if err != nil || reachedProxy.Load() {
		t.Fatalf(
			"proxy boundary failed: reached=%v error=%v",
			reachedProxy.Load(),
			err,
		)
	}
	if download.Size != int64(len(data)) {
		t.Fatal("download content changed")
	}
}
