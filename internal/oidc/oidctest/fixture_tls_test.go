package oidctest_test

import (
	"bytes"
	"net/http"
	"testing"

	"durpdeploy/internal/oidc/oidctest"
)

func TestFixtureGeneratesUniqueTLSCertificate(t *testing.T) {
	// Given
	first := newFixture(t)
	second := newFixture(t)

	// When
	firstCertificate := fixtureCertificate(t, first)
	secondCertificate := fixtureCertificate(t, second)

	// Then
	if bytes.Equal(firstCertificate, secondCertificate) {
		t.Fatal("fixture TLS certificates are identical, want per-fixture keys")
	}
}

func fixtureCertificate(t *testing.T, fixture *oidctest.Fixture) []byte {
	t.Helper()
	response, err := fixture.Client().
		Get(fixture.URL() + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("GET discovery document: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf(
			"discovery status = %d, want %d",
			response.StatusCode,
			http.StatusOK,
		)
	}
	if len(response.TLS.PeerCertificates) != 1 {
		t.Fatalf(
			"peer certificates = %d, want 1",
			len(response.TLS.PeerCertificates),
		)
	}
	return response.TLS.PeerCertificates[0].Raw
}
