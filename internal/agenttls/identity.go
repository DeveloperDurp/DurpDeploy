// Package agenttls creates self-signed agent identities and verifies pinned peers.
package agenttls

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

const (
	CertificateFileName = "identity.crt"
	PrivateKeyFileName  = "identity.key"
)

// Identity is a self-signed TLS certificate with its stable public fingerprint.
type Identity struct {
	Certificate tls.Certificate
	Fingerprint Fingerprint
}

// LoadOrCreate loads an identity from directory or creates one for serverURL.
func LoadOrCreate(directory string, serverURL string) (Identity, error) {
	hostname, err := parseHTTPSURL(serverURL)
	if err != nil {
		return Identity{}, err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return Identity{}, fmt.Errorf("create identity directory: %w", err)
	}
	certPath := filepath.Join(directory, CertificateFileName)
	keyPath := filepath.Join(directory, PrivateKeyFileName)
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		return loadIdentity(certPEM, keyPEM, hostname)
	}
	if certErr != nil && !os.IsNotExist(certErr) {
		return Identity{}, fmt.Errorf("read identity certificate: %w", certErr)
	}
	if keyErr != nil && !os.IsNotExist(keyErr) {
		return Identity{}, fmt.Errorf("read identity private key: %w", keyErr)
	}
	if certErr == nil || keyErr == nil {
		return Identity{}, fmt.Errorf("incomplete identity in %s", directory)
	}
	return createIdentity(certPath, keyPath, hostname)
}

func createIdentity(
	certPath string,
	keyPath string,
	hostname string,
) (Identity, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, fmt.Errorf("generate Ed25519 private key: %w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return Identity{}, fmt.Errorf("generate certificate serial: %w", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hostname},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.AddDate(5, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth,
		},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(hostname); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{hostname}
	}
	certificateDER, err := x509.CreateCertificate(
		rand.Reader,
		template,
		template,
		publicKey,
		privateKey,
	)
	if err != nil {
		return Identity{}, fmt.Errorf("create self-signed certificate: %w", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return Identity{}, fmt.Errorf("marshal private key: %w", err)
	}
	certPEM := pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER},
	)
	keyPEM := pem.EncodeToMemory(
		&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER},
	)
	if err := writePrivateFileAtomically(keyPath, keyPEM, 0o600); err != nil {
		return Identity{}, err
	}
	if err := writePrivateFileAtomically(certPath, certPEM, 0o644); err != nil {
		return Identity{}, err
	}
	return loadIdentity(certPEM, keyPEM, hostname)
}

func loadIdentity(
	certPEM []byte,
	keyPEM []byte,
	hostname string,
) (Identity, error) {
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return Identity{}, fmt.Errorf("load identity key pair: %w", err)
	}
	if len(certificate.Certificate) != 1 {
		return Identity{}, fmt.Errorf(
			"identity must contain exactly one certificate",
		)
	}
	parsed, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return Identity{}, fmt.Errorf("parse identity certificate: %w", err)
	}
	if err := verifySelfSignedCertificate(
		parsed,
		hostname,
		time.Now(),
	); err != nil {
		return Identity{}, fmt.Errorf("validate identity certificate: %w", err)
	}
	if _, ok := certificate.PrivateKey.(ed25519.PrivateKey); !ok {
		return Identity{}, fmt.Errorf("identity private key is not Ed25519")
	}
	return Identity{
		Certificate: certificate,
		Fingerprint: FingerprintOf(parsed.Raw),
	}, nil
}

func writePrivateFileAtomically(
	path string,
	contents []byte,
	mode os.FileMode,
) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temporary identity file: %w", err)
	}
	temporaryPath := file.Name()
	defer func() {
		if removeErr := os.Remove(
			temporaryPath,
		); removeErr != nil && !os.IsNotExist(removeErr) &&
			err == nil {
			err = fmt.Errorf("remove temporary identity file: %w", removeErr)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return fmt.Errorf("set temporary identity file mode: %w", err)
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return fmt.Errorf("write temporary identity file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync temporary identity file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary identity file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace identity file: %w", err)
	}
	return nil
}

func parseHTTPSURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("agent URL must be an HTTPS origin")
	}
	hostname := parsed.Hostname()
	if hostname == "" {
		return "", fmt.Errorf("agent URL must include a hostname")
	}
	return hostname, nil
}
