package agenttls

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"
)

func TestNewClientConfig_acceptsOnlyPinnedValidSelfSignedHostname(
	t *testing.T,
) {
	// Given
	identity, err := LoadOrCreate(t.TempDir(), "https://localhost")
	if err != nil {
		t.Fatalf("create server identity: %v", err)
	}
	address, wait := serveTLS(t, identity.Certificate)
	defer wait()
	serverURL := localURL(t, address)

	// When
	config, err := NewClientConfig(
		serverURL,
		[]Fingerprint{identity.Fingerprint},
	)
	if err != nil {
		t.Fatalf("new client config: %v", err)
	}
	connection, err := tls.Dial("tcp", address, config)
	if err != nil {
		t.Fatalf("dial pinned server: %v", err)
	}
	_ = connection.Close()

}

func TestNewClientConfig_rejectsWrongPinHostnameAndExpiry(t *testing.T) {
	// Given
	valid, err := LoadOrCreate(t.TempDir(), "https://localhost")
	if err != nil {
		t.Fatalf("create server identity: %v", err)
	}
	wrongHost, err := LoadOrCreate(t.TempDir(), "https://other.example.test")
	if err != nil {
		t.Fatalf("create wrong-host identity: %v", err)
	}
	expired := selfSignedCertificate(t, certificateTestOptions{
		hostnames: []string{"localhost"},
		notBefore: time.Now().Add(-2 * time.Hour),
		notAfter:  time.Now().Add(-time.Hour),
	})
	notSelfSigned := certificateSignedByOtherKey(t, "localhost")

	cases := []struct {
		name        string
		certificate tls.Certificate
		pins        []Fingerprint
	}{
		{"wrong pin", valid.Certificate, []Fingerprint{{}}},
		{
			"wrong hostname",
			wrongHost.Certificate,
			[]Fingerprint{wrongHost.Fingerprint},
		},
		{
			"expired",
			expired,
			[]Fingerprint{FingerprintOf(expired.Certificate[0])},
		},
		{
			"not self-signed",
			notSelfSigned,
			[]Fingerprint{FingerprintOf(notSelfSigned.Certificate[0])},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// When
			address, wait := serveTLS(t, test.certificate)
			defer wait()
			config, err := NewClientConfig(localURL(t, address), test.pins)
			if err != nil {
				t.Fatalf("new client config: %v", err)
			}
			connection, err := tls.Dial("tcp", address, config)
			if err == nil {
				_ = connection.Close()
				t.Fatal("invalid peer completed TLS handshake")
			}

		})
	}
}

func TestNewClientConfig_acceptsOldAndNewPinsDuringRotation(t *testing.T) {
	// Given
	oldIdentity, err := LoadOrCreate(t.TempDir(), "https://localhost")
	if err != nil {
		t.Fatalf("create old identity: %v", err)
	}
	newIdentity, err := LoadOrCreate(t.TempDir(), "https://localhost")
	if err != nil {
		t.Fatalf("create new identity: %v", err)
	}
	address, wait := serveTLS(t, newIdentity.Certificate)
	defer wait()

	// When
	config, err := NewClientConfig(
		localURL(t, address),
		[]Fingerprint{oldIdentity.Fingerprint, newIdentity.Fingerprint},
	)
	if err != nil {
		t.Fatalf("new client config: %v", err)
	}
	connection, err := tls.Dial("tcp", address, config)
	if err != nil {
		t.Fatalf("dial rotated server: %v", err)
	}
	_ = connection.Close()
	promoted, err := PromotePendingPin(
		[]Fingerprint{oldIdentity.Fingerprint, newIdentity.Fingerprint},
		newIdentity.Fingerprint,
	)
	if err != nil {
		t.Fatalf("promote pending pin: %v", err)
	}
	if len(promoted) != 1 || promoted[0] != newIdentity.Fingerprint {
		t.Fatal("promotion retained the old fingerprint")
	}
	oldAddress, oldWait := serveTLS(t, oldIdentity.Certificate)
	defer oldWait()
	promotedConfig, err := NewClientConfig(localURL(t, oldAddress), promoted)
	if err != nil {
		t.Fatalf("new promoted config: %v", err)
	}
	oldConnection, err := tls.Dial("tcp", oldAddress, promotedConfig)
	if err == nil {
		_ = oldConnection.Close()
		t.Fatal("promoted configuration accepted old identity")
	}
}

func serveTLS(t *testing.T, certificate tls.Certificate) (string, func()) {
	t.Helper()
	listener, err := tls.Listen(
		"tcp",
		"127.0.0.1:0",
		&tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS13,
		},
	)
	if err != nil {
		t.Fatalf("listen TLS: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			acceptErr = connection.(*tls.Conn).Handshake()
			_ = connection.Close()
		}
		done <- acceptErr
	}()
	return listener.Addr().String(), func() {
		t.Helper()
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
		<-done
	}
}

type certificateTestOptions struct {
	hostnames []string
	notBefore time.Time
	notAfter  time.Time
}

func selfSignedCertificate(
	t *testing.T,
	options certificateTestOptions,
) tls.Certificate {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(
			1,
		), Subject: pkix.Name{CommonName: options.hostnames[0]},
		DNSNames: options.hostnames, NotBefore: options.notBefore, NotAfter: options.notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(
		rand.Reader,
		template,
		template,
		publicKey,
		privateKey,
	)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey}
}

func certificateSignedByOtherKey(
	t *testing.T,
	hostname string,
) tls.Certificate {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	_, signingKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: hostname},
		DNSNames: []string{
			hostname,
		}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(
		rand.Reader,
		template,
		template,
		publicKey,
		signingKey,
	)
	if err != nil {
		t.Fatalf("create certificate signed by other key: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey}
}

func localURL(t *testing.T, address string) string {
	return urlForHost(t, address, "localhost")
}

func urlForHost(t *testing.T, address string, hostname string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split host: %v", err)
	}
	return "https://" + hostname + ":" + port
}
