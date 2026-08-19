package agentpayload

import (
	"testing"

	"durpdeploy/internal/agenttls"
)

func TestEnvelope_opensOnlyForRecipientAndDeployment(t *testing.T) {
	// Given
	recipient := testIdentity(t)
	other := testIdentity(t)
	plaintext := []byte(`{"deployment_id":42,"variables":[{"value":"secret"}]}`)
	public, err := recipientPublicKey(recipient.Certificate.Certificate[0])
	if err != nil {
		t.Fatalf("convert recipient public key: %v", err)
	}
	private, err := x25519PrivateKey(recipient)
	if err != nil {
		t.Fatalf("convert recipient private key: %v", err)
	}
	if string(public.Bytes()) != string(private.PublicKey().Bytes()) {
		t.Fatal("converted Ed25519 public and private keys differ")
	}
	envelope, err := Seal(recipient.Certificate.Certificate[0], 42, plaintext)
	if err != nil {
		t.Fatalf("seal envelope: %v", err)
	}

	// When
	decoded, err := Open(recipient, 42, envelope)

	// Then
	if err != nil || string(decoded) != string(plaintext) {
		t.Fatalf("open envelope = %q, %v", decoded, err)
	}
	if _, err := Open(other, 42, envelope); err == nil {
		t.Fatal("different identity opened envelope")
	}
	if _, err := Open(recipient, 43, envelope); err == nil {
		t.Fatal("different deployment opened envelope")
	}
}

func TestEnvelope_rejectsTampering(t *testing.T) {
	// Given
	identity := testIdentity(t)
	envelope, err := Seal(
		identity.Certificate.Certificate[0],
		42,
		[]byte("payload"),
	)
	if err != nil {
		t.Fatalf("seal envelope: %v", err)
	}
	tampered := append([]byte(nil), envelope...)
	tampered[len(tampered)-1] ^= 1

	// When
	_, err = Open(identity, 42, tampered)

	// Then
	if err == nil {
		t.Fatal("tampered envelope opened")
	}
}

func testIdentity(t *testing.T) agenttls.Identity {
	t.Helper()
	identity, err := agenttls.LoadOrCreate(t.TempDir(), "https://127.0.0.1")
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	return identity
}
