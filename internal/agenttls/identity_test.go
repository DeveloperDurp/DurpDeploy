package agenttls

import (
	"crypto/ed25519"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadOrCreate_generatesReloadsAndSecuresIdentity(t *testing.T) {
	// Given
	directory := t.TempDir()
	serverURL := "https://agent.example.test:10943"

	// When
	created, err := LoadOrCreate(directory, serverURL)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	reloaded, err := LoadOrCreate(directory, serverURL)
	if err != nil {
		t.Fatalf("reload identity: %v", err)
	}

	// Then
	if created.Fingerprint != reloaded.Fingerprint {
		t.Fatal("reload generated a different identity")
	}
	certificate, err := x509.ParseCertificate(
		created.Certificate.Certificate[0],
	)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	if err := certificate.VerifyHostname("agent.example.test"); err != nil {
		t.Fatalf("hostname SAN: %v", err)
	}
	if certificate.NotAfter.Sub(
		certificate.NotBefore,
	) < 5*365*24*time.Hour-time.Hour {
		t.Fatalf(
			"certificate validity = %s, want approximately five years",
			certificate.NotAfter.Sub(certificate.NotBefore),
		)
	}
	if _, ok := created.Certificate.PrivateKey.(ed25519.PrivateKey); !ok {
		t.Fatalf(
			"private key type = %T, want ed25519.PrivateKey",
			created.Certificate.PrivateKey,
		)
	}
	assertFileMode(t, filepath.Join(directory, PrivateKeyFileName), 0o600)
	assertFileMode(t, filepath.Join(directory, CertificateFileName), 0o644)
}

func TestLoadOrCreate_rejectsInvalidURLAndMismatchedKey(t *testing.T) {
	// Given
	directory := t.TempDir()

	// When / Then
	if _, err := LoadOrCreate(
		directory,
		"http://agent.example.test",
	); err == nil {
		t.Fatal("HTTP URL accepted")
	}
	if _, err := LoadOrCreate(
		directory,
		"https://agent.example.test",
	); err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	otherDirectory := t.TempDir()
	if _, err := LoadOrCreate(
		otherDirectory,
		"https://agent.example.test",
	); err != nil {
		t.Fatalf("generate other identity: %v", err)
	}
	otherKey, err := os.ReadFile(
		filepath.Join(otherDirectory, PrivateKeyFileName),
	)
	if err != nil {
		t.Fatalf("read other private key: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, PrivateKeyFileName), otherKey, 0o600,
	); err != nil {
		t.Fatalf("replace private key: %v", err)
	}
	if _, err := LoadOrCreate(
		directory,
		"https://agent.example.test",
	); err == nil {
		t.Fatal("different private key accepted")
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode %s = %04o, want %04o", path, got, want)
	}
}
