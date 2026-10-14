//go:build e2e && dbtls && linux

package api_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type databaseTLSCertificates struct {
	ca, otherCA, cert, key string
}

func databaseTLSCerts(t *testing.T) databaseTLSCertificates {
	t.Helper()
	root := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(
			1,
		), Subject: pkix.Name{CommonName: "Database test CA"},
		NotBefore: time.Now().
			Add(-time.Minute),
		NotAfter: time.Now().Add(time.Hour),
		IsCA:     true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(
		rand.Reader,
		ca,
		ca,
		&key.PublicKey,
		key,
	)
	if err != nil {
		t.Fatal(err)
	}
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(
			2,
		), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames:  []string{"localhost"},
		NotBefore: ca.NotBefore, NotAfter: ca.NotAfter,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, err := x509.CreateCertificate(
		rand.Reader,
		cert,
		ca,
		&serverKey.PublicKey,
		key,
	)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	otherDER, err := x509.CreateCertificate(
		rand.Reader,
		ca,
		ca,
		&otherKey.PublicKey,
		otherKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, kind string, der []byte) string {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	return databaseTLSCertificates{
		ca:      write("ca.pem", "CERTIFICATE", caDER),
		otherCA: write("other-ca.pem", "CERTIFICATE", otherDER),
		cert:    write("server.pem", "CERTIFICATE", certDER),
		key:     write("server.key", "PRIVATE KEY", keyDER),
	}
}
