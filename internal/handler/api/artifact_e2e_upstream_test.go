//go:build e2e

package api_test

import (
	"archive/zip"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/artifact"
)

func artifactHTTPSFixture(t *testing.T) (string, *artifact.Client, func()) {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var ip net.IP
	for _, address := range addresses {
		candidate, _, err := net.ParseCIDR(address.String())
		if err == nil && candidate.To4() != nil &&
			candidate.IsGlobalUnicast() &&
			!candidate.IsLoopback() {
			ip = candidate
			break
		}
	}
	if ip == nil {
		t.Fatal("artifact E2E requires a non-loopback interface")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{ip},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(
		rand.Reader,
		template,
		template,
		&key.PublicKey,
		key,
	)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.RWMutex
	contents := "package"
	srv := httptest.NewUnstartedServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer artifact-secret" {
				w.WriteHeader(401)
				return
			}
			mu.RLock()
			text := contents
			mu.RUnlock()
			var buffer bytes.Buffer
			writer := zip.NewWriter(&buffer)
			entry, err := writer.Create("app.txt")
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := io.WriteString(entry, text); err != nil {
				t.Error(err)
				return
			}
			if err := writer.Close(); err != nil {
				t.Error(err)
				return
			}
			if _, err := w.Write(buffer.Bytes()); err != nil {
				t.Error(err)
			}
		}),
	)
	if err := srv.Listener.Close(); err != nil {
		t.Fatal(err)
	}
	srv.Listener, err = net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{
			{Certificate: [][]byte{der}, PrivateKey: key},
		},
		MinVersion: tls.VersionTLS12,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	client := artifact.NewClient()
	client.HTTP.Transport.(*http.Transport).TLSClientConfig = &tls.Config{
		RootCAs:    roots,
		MinVersion: tls.VersionTLS12,
	}
	return srv.URL, client, func() { mu.Lock(); contents = "republished"; mu.Unlock() }
}
