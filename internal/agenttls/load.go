package agenttls

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LoadExisting loads a previously paired agent identity without creating a
// replacement identity when protected state is incomplete.
func LoadExisting(directory string) (Identity, error) {
	certPEM, err := os.ReadFile(filepath.Join(directory, CertificateFileName))
	if err != nil {
		return Identity{}, fmt.Errorf(
			"read agent identity certificate: %w",
			err,
		)
	}
	keyPEM, err := os.ReadFile(filepath.Join(directory, PrivateKeyFileName))
	if err != nil {
		return Identity{}, fmt.Errorf(
			"read agent identity private key: %w",
			err,
		)
	}
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil || len(certificate.Certificate) != 1 {
		return Identity{}, fmt.Errorf("load agent identity key pair")
	}
	parsed, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return Identity{}, fmt.Errorf(
			"parse agent identity certificate: %w",
			err,
		)
	}
	if err := verifySelfSignedIdentity(parsed, time.Now()); err != nil {
		return Identity{}, fmt.Errorf(
			"validate agent identity certificate: %w",
			err,
		)
	}
	if _, ok := certificate.PrivateKey.(ed25519.PrivateKey); !ok {
		return Identity{}, fmt.Errorf(
			"agent identity private key is not Ed25519",
		)
	}
	return Identity{
		Certificate: certificate,
		Fingerprint: FingerprintOf(parsed.Raw),
	}, nil
}
