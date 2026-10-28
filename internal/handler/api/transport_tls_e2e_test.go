//go:build e2e && transportsecurity

package api_test

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"strings"
	"testing"

	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestTransportTLSAPIWebE2E(t *testing.T) {
	f := loadTransportFixture(t)
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		t.Run(tls.VersionName(version), func(t *testing.T) {
			client := f.client(t)
			config := client.Transport.(*http.Transport).TLSClientConfig
			config.MinVersion, config.MaxVersion = version, version
			res, _ := transportResponse(t, client,
				transportRequest(t, "GET", f.URL+"/api/v1/healthz", ""), 200)
			if res.TLS == nil || res.TLS.Version != version {
				t.Fatal("TLS policy did not negotiate the required version")
			}
		})
	}
	for _, invalid := range []string{"old-protocol", "wrong-name", "untrusted-ca"} {
		t.Run(invalid, func(t *testing.T) {
			client := f.client(t)
			config := client.Transport.(*http.Transport).TLSClientConfig
			switch invalid {
			case "old-protocol":
				config.MinVersion, config.MaxVersion = tls.VersionTLS10, tls.VersionTLS11
			case "wrong-name":
				config.ServerName = "untrusted.invalid"
			case "untrusted-ca":
				config.RootCAs = x509.NewCertPool()
			}
			res, err := client.Do(transportRequest(t, "GET",
				f.URL+"/api/v1/healthz", ""))
			if res != nil {
				res.Body.Close()
			}
			if err == nil {
				t.Fatal("invalid TLS connection was accepted")
			}
		})
	}
	transportResponse(t, f.client(t), transportRequest(t, "GET",
		f.URL+"/api/v1/healthz", ""), 200)
}

func TestTransportAgentBoundaryE2E(t *testing.T) {
	f := loadTransportFixture(t)
	ca, err := os.ReadFile(f.AgentCA)
	if err != nil {
		t.Fatal("agent public certificate evidence is required")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid agent public certificate")
	}
	unpaired, err := agenttls.LoadOrCreate(
		t.TempDir(),
		"https://unpaired.invalid",
	)
	if err != nil {
		t.Fatal("could not create the unpaired test identity")
	}
	for _, mode := range []string{"no-certificate", "unpaired", "tls12"} {
		t.Run(mode, func(t *testing.T) {
			client := f.client(t)
			config := client.Transport.(*http.Transport).TLSClientConfig
			config.RootCAs = roots
			config.MinVersion = tls.VersionTLS13
			serverVerified := false
			config.VerifyConnection = func(tls.ConnectionState) error {
				serverVerified = true
				return nil
			}
			if mode != "no-certificate" {
				config.Certificates = []tls.Certificate{unpaired.Certificate}
			}
			if mode == "tls12" {
				config.MinVersion, config.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
			}
			res, err := client.Do(transportRequest(t, "POST",
				f.AgentURL+"/agent/v1/poll", ""))
			if res != nil {
				res.Body.Close()
			}
			if err == nil ||
				!strings.Contains(err.Error(), "remote error: tls:") ||
				(mode != "tls12" && !serverVerified) {
				t.Fatal(
					"agent rejection did not prove the expected TLS boundary",
				)
			}
		})
	}
}
