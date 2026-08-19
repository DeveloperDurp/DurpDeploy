// Package agentpayload seals deployment payloads to an enrolled agent identity.
package agentpayload

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"durpdeploy/internal/agenttls"
)

const version = "agent-envelope/1"

const nonceSize = 12

var curve25519Prime = new(
	big.Int,
).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))

type envelope struct {
	Version         string `json:"version"`
	EphemeralPublic string `json:"ephemeral_public"`
	WrappedKeyNonce string `json:"wrapped_key_nonce"`
	WrappedKey      string `json:"wrapped_key"`
	PayloadNonce    string `json:"payload_nonce"`
	Ciphertext      string `json:"ciphertext"`
}

// Seal encrypts plaintext for the Ed25519 public key in certificateDER.
func Seal(
	certificateDER []byte,
	deploymentID int64,
	plaintext []byte,
) ([]byte, error) {
	if deploymentID < 1 {
		return nil, errors.New("deployment ID must be positive")
	}
	recipient, err := recipientPublicKey(certificateDER)
	if err != nil {
		return nil, err
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate envelope key: %w", err)
	}
	shared, err := ephemeral.ECDH(recipient)
	if err != nil {
		return nil, fmt.Errorf("derive envelope key: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate payload key: %w", err)
	}
	wrappedNonce, wrappedKey, err := seal(
		keyEncryptionKey(shared),
		aad(deploymentID),
		key,
	)
	if err != nil {
		return nil, err
	}
	payloadNonce, ciphertext, err := seal(key, aad(deploymentID), plaintext)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(envelope{
		Version: version,
		EphemeralPublic: base64.RawStdEncoding.EncodeToString(
			ephemeral.PublicKey().Bytes(),
		),
		WrappedKeyNonce: base64.RawStdEncoding.EncodeToString(wrappedNonce),
		WrappedKey:      base64.RawStdEncoding.EncodeToString(wrappedKey),
		PayloadNonce:    base64.RawStdEncoding.EncodeToString(payloadNonce),
		Ciphertext:      base64.RawStdEncoding.EncodeToString(ciphertext),
	})
	if err != nil {
		return nil, fmt.Errorf("encode envelope: %w", err)
	}
	return encoded, nil
}

// Open decrypts an envelope using the agent identity private key.
func Open(
	identity agenttls.Identity,
	deploymentID int64,
	encoded []byte,
) ([]byte, error) {
	if deploymentID < 1 {
		return nil, errors.New("deployment ID must be positive")
	}
	var decoded envelope
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, errors.New("invalid deployment envelope")
	}
	if decoded.Version != version {
		return nil, errors.New("unsupported deployment envelope")
	}
	ephemeralBytes, err := decode(decoded.EphemeralPublic, 32)
	if err != nil {
		return nil, err
	}
	private, err := x25519PrivateKey(identity)
	if err != nil {
		return nil, err
	}
	ephemeral, err := ecdh.X25519().NewPublicKey(ephemeralBytes)
	if err != nil {
		return nil, errors.New("invalid deployment envelope")
	}
	shared, err := private.ECDH(ephemeral)
	if err != nil {
		return nil, errors.New("invalid deployment envelope")
	}
	wrappedNonce, err := decode(decoded.WrappedKeyNonce, nonceSize)
	if err != nil {
		return nil, err
	}
	wrappedKey, err := decodeAtLeast(decoded.WrappedKey, 16)
	if err != nil {
		return nil, err
	}
	key, err := open(
		keyEncryptionKey(shared),
		aad(deploymentID),
		wrappedNonce,
		wrappedKey,
	)
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid deployment envelope")
	}
	payloadNonce, err := decode(decoded.PayloadNonce, nonceSize)
	if err != nil {
		return nil, err
	}
	ciphertext, err := decodeAtLeast(decoded.Ciphertext, 16)
	if err != nil {
		return nil, err
	}
	plaintext, err := open(key, aad(deploymentID), payloadNonce, ciphertext)
	if err != nil {
		return nil, errors.New("invalid deployment envelope")
	}
	return plaintext, nil
}

func recipientPublicKey(certificateDER []byte) (*ecdh.PublicKey, error) {
	certificate, err := x509.ParseCertificate(certificateDER)
	if err != nil {
		return nil, errors.New("invalid agent certificate")
	}
	public, ok := certificate.PublicKey.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("agent certificate is not Ed25519")
	}
	converted, err := ed25519PublicToX25519(public)
	if err != nil {
		return nil, err
	}
	key, err := ecdh.X25519().NewPublicKey(converted)
	if err != nil {
		return nil, errors.New("invalid agent certificate")
	}
	return key, nil
}

func x25519PrivateKey(identity agenttls.Identity) (*ecdh.PrivateKey, error) {
	private, ok := identity.Certificate.PrivateKey.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("agent identity is not Ed25519")
	}
	digest := sha512.Sum512(private.Seed())
	digest[0] &= 248
	digest[31] &= 63
	digest[31] |= 64
	key, err := ecdh.X25519().NewPrivateKey(digest[:32])
	if err != nil {
		return nil, errors.New("invalid agent identity")
	}
	return key, nil
}

func ed25519PublicToX25519(public ed25519.PublicKey) ([]byte, error) {
	if len(public) != ed25519.PublicKeySize {
		return nil, errors.New("invalid agent certificate")
	}
	littleEndian := append([]byte(nil), public...)
	littleEndian[31] &= 127
	reverse(littleEndian)
	y := new(big.Int).SetBytes(littleEndian)
	if y.Cmp(curve25519Prime) >= 0 {
		return nil, errors.New("invalid agent certificate")
	}
	denominator := new(big.Int).Sub(big.NewInt(1), y)
	denominator.Mod(denominator, curve25519Prime)
	inverse := new(big.Int).ModInverse(denominator, curve25519Prime)
	if inverse == nil {
		return nil, errors.New("invalid agent certificate")
	}
	u := new(big.Int).Add(big.NewInt(1), y)
	u.Mul(u, inverse)
	u.Mod(u, curve25519Prime)
	converted := make([]byte, 32)
	u.FillBytes(converted)
	reverse(converted)
	return converted, nil
}

func seal(
	key []byte,
	additionalData []byte,
	plaintext []byte,
) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("create deployment cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("create deployment cipher: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("generate deployment nonce: %w", err)
	}
	return nonce, aead.Seal(nil, nonce, plaintext, additionalData), nil
}

func open(
	key []byte,
	additionalData, nonce, ciphertext []byte,
) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nonce, ciphertext, additionalData)
}

func keyEncryptionKey(shared []byte) []byte {
	digest := sha256.Sum256(append([]byte(version), shared...))
	return digest[:]
}

func aad(deploymentID int64) []byte {
	return []byte(fmt.Sprintf("%s:%d", version, deploymentID))
}

func decode(encoded string, length int) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != length {
		return nil, errors.New("invalid deployment envelope")
	}
	return decoded, nil
}

func decodeAtLeast(encoded string, minimum int) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) < minimum {
		return nil, errors.New("invalid deployment envelope")
	}
	return decoded, nil
}

func reverse(value []byte) {
	for left, right := 0, len(value)-1; left < right; left, right = left+1, right-1 {
		value[left], value[right] = value[right], value[left]
	}
}
