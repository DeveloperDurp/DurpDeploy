//go:build e2e

package api_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/handler"
	"durpdeploy/internal/testutil"
	containertypes "github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type proxyBodyCase struct {
	base, name string
	client     *http.Client
	protocol   int
	expect     bool
	api        bool
	above      bool
}

func TestProxyBodyAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	direct := httptest.NewUnstartedServer(f.server.Config.Handler)
	direct.EnableHTTP2 = true
	direct.StartTLS()
	t.Cleanup(direct.Close)
	proxy := startBodyCaddy(t, f.server, direct.TLS.Certificates[0])
	for mode, base := range map[string]string{"direct": direct.URL, "proxy": proxy} {
		for _, protocol := range []int{1, 2} {
			client := bodyProxyClient(t, direct.Certificate(), protocol)
			for _, expect := range []bool{false, true} {
				checkProxyBodyVariants(t, f, proxyBodyCase{
					base: base, client: client, protocol: protocol, expect: expect,
					name: fmt.Sprintf(
						"%s-h%d-expect%t",
						mode,
						protocol,
						expect,
					),
				})
			}
		}
	}
}

func checkProxyBodyVariants(
	t *testing.T,
	f *artifactE2E,
	scenario proxyBodyCase,
) {
	t.Helper()
	for _, api := range []bool{true, false} {
		for _, above := range []bool{true, false} {
			scenario.api, scenario.above = api, above
			t.Run(fmt.Sprintf("%s-api%t-above%t", scenario.name, api, above),
				func(t *testing.T) { checkProxyBodyBoundary(t, f, scenario) })
		}
	}
	checkProxyBodyControls(t, f, scenario)
}

type proxyBodyControl struct {
	name, path         string
	status             int
	authenticated, api bool
}

func checkProxyBodyControls(
	t *testing.T,
	f *artifactE2E,
	scenario proxyBodyCase,
) {
	t.Helper()
	for _, check := range []proxyBodyControl{
		{"control", "/api/v1/admin/maintenance", 413, true, true},
		{"authentication", "/api/v1/projects", 401, false, true},
		{"csrf", "/projects", 403, true, false},
	} {
		t.Run(scenario.name+"-"+check.name, func(t *testing.T) {
			checkProxyBodyControlRequest(t, f, scenario, check)
		})
	}
}

func checkProxyBodyControlRequest(
	t *testing.T,
	f *artifactE2E,
	scenario proxyBodyCase,
	check proxyBodyControl,
) {
	t.Helper()
	before, audits := proxyBodyState(t, f)
	body := "name=csrf-rejected"
	contentType := "application/x-www-form-urlencoded"
	if check.api {
		contentType = "application/json"
		body = "{}" + strings.Repeat(
			" ",
			int(handler.MaxJSONBodyBytes)-1,
		)
	}
	request, err := http.NewRequestWithContext(t.Context(), "POST",
		scenario.base+check.path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", contentType)
	scenario.api = check.api
	setProxyBodyIdentity(request, f, scenario)
	if !check.authenticated {
		request.Header.Del("Authorization")
	}
	request.Header.Del("X-CSRF-Token")
	response, err := scenario.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != check.status ||
		response.ProtoMajor != scenario.protocol {
		t.Fatalf(
			"status=%d protocol=%s",
			response.StatusCode,
			response.Proto,
		)
	}
	after, afterAudits := proxyBodyState(t, f)
	if before != after || audits != afterAudits {
		t.Fatal(
			"rejected control request mutated projects or successful audit state",
		)
	}
	checkProxyBodyHealth(t, scenario)
}

func checkProxyBodyBoundary(
	t *testing.T,
	f *artifactE2E,
	scenario proxyBodyCase,
) {
	t.Helper()
	before, audits := proxyBodyState(t, f)
	limit := handler.MaxFormBodyBytes
	contentType := "application/x-www-form-urlencoded"
	path := "/projects"
	body := "name=" + scenario.name + "-web&padding="
	wantStatus, wantError := 303, "Request body too large\n"
	if scenario.api {
		limit = handler.MaxJSONBodyBytes
		contentType, path = "application/json", "/api/v1/projects"
		body = fmt.Sprintf(`{"name":%q}`, scenario.name+"-api")
		wantStatus, wantError = 201, "{\"error\":\"Request body too large\"}\n"
	}
	if scenario.above {
		limit++
		wantStatus = 413
	}
	request, err := http.NewRequestWithContext(
		t.Context(),
		"POST",
		scenario.base+path,
		strings.NewReader(body+strings.Repeat(" ", int(limit)-len(body))),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", contentType)
	setProxyBodyIdentity(request, f, scenario)
	var continued atomic.Bool
	request = request.WithContext(httptrace.WithClientTrace(
		request.Context(), &httptrace.ClientTrace{
			Got100Continue: func() { continued.Store(true) },
		},
	))
	response, err := scenario.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	response.Body.Close()
	if err != nil || response.StatusCode != wantStatus ||
		response.ProtoMajor != scenario.protocol {
		t.Fatalf(
			"status=%d protocol=%s body=%q err=%v",
			response.StatusCode,
			response.Proto,
			data,
			err,
		)
	}
	mutations := int64(1)
	if scenario.above {
		mutations = 0
		if string(data) != wantError {
			t.Fatalf("unstable rejection: %q", data)
		}
	}
	if scenario.protocol == 1 && scenario.above && scenario.expect &&
		continued.Load() {
		t.Fatal("oversized request received continue before rejection")
	}
	after, afterAudits := proxyBodyState(t, f)
	if after != before+mutations || afterAudits != audits+mutations {
		t.Fatalf(
			"projects %d -> %d; audits %d -> %d",
			before,
			after,
			audits,
			afterAudits,
		)
	}
	checkProxyBodyHealth(t, scenario)
}

func setProxyBodyIdentity(
	request *http.Request,
	f *artifactE2E,
	scenario proxyBodyCase,
) {
	if scenario.api {
		request.Header.Set("Authorization", "Bearer "+f.token)
	} else {
		request.AddCookie(&http.Cookie{Name: "session", Value: f.session})
		request.Header.Set("X-CSRF-Token", f.csrf)
	}
	if scenario.expect {
		request.Header.Set("Expect", "100-continue")
	}
}

func proxyBodyState(t *testing.T, f *artifactE2E) (int64, int64) {
	t.Helper()
	var count int64
	if err := f.h.repo.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM projects").Scan(&count); err != nil {
		t.Fatal(err)
	}
	audits, err := f.h.repo.Queries.CountAuditLogs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return count, audits
}

func checkProxyBodyHealth(t *testing.T, scenario proxyBodyCase) {
	t.Helper()
	response, err := scenario.client.Get(scenario.base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("health status=%d", response.StatusCode)
	}
}

func bodyProxyClient(
	t *testing.T,
	certificate *x509.Certificate,
	protocol int,
) *http.Client {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(protocol == 2)
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:    roots,
			MinVersion: tls.VersionTLS12,
			ServerName: certificate.DNSNames[0],
		},
		Protocols: protocols, ExpectContinueTimeout: time.Second,
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func startBodyCaddy(
	t *testing.T,
	upstream *httptest.Server,
	certificate tls.Certificate,
) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	config, err := exec.CommandContext(
		t.Context(),
		"bash",
		"../../../scripts/https_proxy_config.sh",
		":"+port,
		"/demo/cert.pem",
		"/demo/key.pem",
		upstream.URL,
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"Caddyfile": append(
			[]byte(
				"{\n admin off\n auto_https off\n default_bind 127.0.0.1\n}\n",
			),
			config...),
		"cert.pem": pem.EncodeToMemory(
			&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]},
		),
		"key.pem": pem.EncodeToMemory(
			&pem.Block{Type: "PRIVATE KEY", Bytes: key},
		),
	}
	var containerFiles []testcontainers.ContainerFile
	for name, data := range files {
		containerFiles = append(containerFiles, testcontainers.ContainerFile{
			Reader: bytes.NewReader(
				data,
			), ContainerFilePath: "/demo/" + name, FileMode: 0600,
		})
	}
	container, err := testcontainers.GenericContainer(
		t.Context(),
		testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image: "harbor.durp.info/dockerhub/library/caddy:2-alpine", ExposedPorts: []string{port + "/tcp"},
				Files: containerFiles,
				HostConfigModifier: func(config *containertypes.HostConfig) {
					// The SSH host-access tunnel closes both directions at the
					// first EOF. Early HTTP rejections need native TCP half-close.
					config.NetworkMode = "host"
					config.CapDrop = []string{"ALL"}
					config.CapAdd = []string{"NET_BIND_SERVICE"}
					config.SecurityOpt = []string{"no-new-privileges:true"}
				},
				Cmd: []string{
					"caddy",
					"run",
					"--config",
					"/demo/Caddyfile",
					"--adapter",
					"caddyfile",
				},
				WaitingFor: wait.ForListeningPort(port + "/tcp").
					SkipInternalCheck().
					WithStartupTimeout(time.Minute),
			}, Started: true,
		},
	)
	testutil.CleanupContainer(t, container)
	if err != nil {
		t.Fatal(err)
	}
	host, err := container.Host(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	proxyPort, err := container.MappedPort(t.Context(), port+"/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return "https://" + net.JoinHostPort(host, proxyPort.Port())
}
