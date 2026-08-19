package agenttls

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"time"
)

// Fingerprint is the SHA-256 digest of an X.509 certificate's DER bytes.
type Fingerprint [sha256.Size]byte

// FingerprintOf returns the stable pin for a DER-encoded certificate.
func FingerprintOf(certificateDER []byte) Fingerprint {
	return sha256.Sum256(certificateDER)
}

func (fingerprint Fingerprint) String() string {
	return hex.EncodeToString(fingerprint[:])
}

// ParseFingerprint parses a hexadecimal SHA-256 certificate fingerprint.
func ParseFingerprint(raw string) (Fingerprint, error) {
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != sha256.Size {
		return Fingerprint{}, fmt.Errorf(
			"invalid SHA-256 certificate fingerprint",
		)
	}
	var fingerprint Fingerprint
	copy(fingerprint[:], decoded)
	return fingerprint, nil
}

// NewClientConfig returns a TLS configuration that accepts only the pinned peer.
func NewClientConfig(
	serverURL string,
	pins []Fingerprint,
) (*tls.Config, error) {
	hostname, err := parseHTTPSURL(serverURL)
	if err != nil {
		return nil, err
	}
	if len(pins) == 0 {
		return nil, fmt.Errorf("at least one server fingerprint is required")
	}
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		ServerName:         hostname,
		InsecureSkipVerify: true, // Custom verification below checks the complete peer contract.
		VerifyConnection: func(connection tls.ConnectionState) error {
			if len(connection.PeerCertificates) != 1 {
				return fmt.Errorf("peer must provide exactly one certificate")
			}
			certificate := connection.PeerCertificates[0]
			if !matchesPin(FingerprintOf(certificate.Raw), pins) {
				return fmt.Errorf("peer certificate fingerprint is not pinned")
			}
			if err := verifySelfSignedCertificate(
				certificate,
				hostname,
				time.Now(),
			); err != nil {
				return fmt.Errorf("verify peer certificate: %w", err)
			}
			return nil
		},
	}, nil
}

// PromotePendingPin removes the old pin only after a verified pending-pin connection.
func PromotePendingPin(
	pins []Fingerprint,
	connected Fingerprint,
) ([]Fingerprint, error) {
	if len(pins) != 2 {
		return nil, fmt.Errorf(
			"rotation requires exactly old and pending fingerprints",
		)
	}
	if subtle.ConstantTimeCompare(pins[1][:], connected[:]) != 1 {
		return nil, fmt.Errorf(
			"connected fingerprint is not the pending fingerprint",
		)
	}
	return []Fingerprint{pins[1]}, nil
}

func matchesPin(actual Fingerprint, pins []Fingerprint) bool {
	for _, pin := range pins {
		if subtle.ConstantTimeCompare(actual[:], pin[:]) == 1 {
			return true
		}
	}
	return false
}

func verifySelfSignedCertificate(
	certificate *x509.Certificate,
	hostname string,
	now time.Time,
) error {
	if now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) {
		return fmt.Errorf("certificate is outside its validity window")
	}
	if certificate.SignatureAlgorithm != x509.PureEd25519 {
		return fmt.Errorf("certificate signature is not Ed25519")
	}
	if _, ok := certificate.PublicKey.(ed25519.PublicKey); !ok {
		return fmt.Errorf("certificate public key is not Ed25519")
	}
	if err := certificate.CheckSignature(
		certificate.SignatureAlgorithm,
		certificate.RawTBSCertificate,
		certificate.Signature,
	); err != nil {
		return fmt.Errorf("certificate is not self-signed: %w", err)
	}
	if err := certificate.VerifyHostname(hostname); err != nil {
		return fmt.Errorf("certificate hostname: %w", err)
	}
	return nil
}
