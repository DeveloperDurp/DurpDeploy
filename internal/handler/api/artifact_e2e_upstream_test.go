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
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/artifact"
)

func artifactHTTPSFixture(
	t *testing.T,
) (string, *artifact.Client, func(string), func(string)) {
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
	credential := "artifact-secret"
	srv := httptest.NewUnstartedServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.RLock()
			expectedCredential := credential
			text := contents
			mu.RUnlock()
			if r.Header.Get("Authorization") != "Bearer "+expectedCredential {
				w.WriteHeader(401)
				return
			}
			if text == "missing" {
				w.WriteHeader(404)
				return
			}
			if text == "invalid" {
				if _, err := io.WriteString(w, "not a ZIP"); err != nil {
					t.Error(err)
				}
				return
			}
			if strings.HasPrefix(text, "file:") ||
				strings.HasPrefix(text, "blocked:") {
				serveArtifactArchive(t, w, r, text)
				return
			}
			var buffer bytes.Buffer
			writer := zip.NewWriter(&buffer)
			if text == "metadata-heavy" {
				comment := strings.Repeat("x", 65535)
				for i := 0; i < 300; i++ {
					if _, err := writer.CreateHeader(
						&zip.FileHeader{
							Name:    fmt.Sprintf("item-%d", i),
							Comment: comment,
						},
					); err != nil {
						t.Error(err)
						return
					}
				}
			} else {
				name := "app.txt"
				if text == "long-name" {
					name = strings.Repeat("n", 256)
				}
				entry, err := writer.Create(name)
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := io.WriteString(entry, text); err != nil {
					t.Error(err)
					return
				}
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
	return srv.URL, client,
		func(value string) { mu.Lock(); contents = value; mu.Unlock() },
		func(value string) { mu.Lock(); credential = value; mu.Unlock() }
}

func serveArtifactArchive(
	t *testing.T,
	w http.ResponseWriter,
	r *http.Request,
	source string,
) {
	t.Helper()
	mode, filename, _ := strings.Cut(source, ":")
	if mode == "file" {
		http.ServeFile(w, r, filename)
		return
	}
	file, err := os.Open(filename)
	if err != nil {
		t.Error(err)
		w.WriteHeader(500)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Error(err)
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	if _, err := io.CopyN(w, file, 1024); err != nil {
		t.Error(err)
		return
	}
	w.(http.Flusher).Flush()
	// A controlled partial response holds the deployment in download until its
	// owning server process is killed. The request context then releases this.
	<-r.Context().Done()
}
