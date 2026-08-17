package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (f *Fixture) tokenResponse(nonce string) ([]byte, error) {
	f.mu.RLock()
	mode := f.tokenMode
	f.mu.RUnlock()
	response := tokenResponse{
		AccessToken: "fixture-access",
		TokenType:   "Bearer",
	}
	switch mode {
	case TokenMissing:
		return json.Marshal(response)
	case TokenNonString:
		response.IDToken = json.RawMessage("1")
		return json.Marshal(response)
	default:
		token, err := f.signToken(nonce, mode)
		if err != nil {
			return nil, err
		}
		response.IDToken, err = json.Marshal(token)
		if err != nil {
			return nil, fmt.Errorf("marshal fixture ID token: %w", err)
		}
		return json.Marshal(response)
	}
}

func (f *Fixture) signToken(nonce string, mode TokenMode) (string, error) {
	f.mu.RLock()
	claims := cloneClaims(f.claims)
	privateKey := f.privateKey
	keyID := f.keyID
	f.mu.RUnlock()
	issuer := f.URL()
	audience := f.clientID
	expiresAt := f.now.Add(time.Hour).Unix()
	switch mode {
	case TokenBadSignature:
	case TokenWrongIssuer:
		issuer += "/unexpected"
	case TokenWrongAudience:
		audience = "unexpected-client"
	case TokenExpired:
		expiresAt = f.now.Add(-time.Hour).Unix()
	case TokenWrongNonce:
		nonce = "unexpected-nonce"
	}
	header, err := json.Marshal(jwtHeader{Algorithm: "RS256", KeyID: keyID})
	if err != nil {
		return "", fmt.Errorf("marshal fixture JWT header: %w", err)
	}
	var authTime *int64
	if !claims.AuthTime.IsZero() {
		value := claims.AuthTime.Unix()
		authTime = &value
	}
	payload, err := json.Marshal(jwtClaims{
		Issuer:        issuer,
		Audience:      audience,
		Subject:       claims.Subject,
		Nonce:         nonce,
		ExpiresAt:     expiresAt,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		Groups:        claims.Groups,
		AuthTime:      authTime,
	})
	if err != nil {
		return "", fmt.Errorf("marshal fixture JWT claims: %w", err)
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(
		rand.Reader,
		privateKey,
		crypto.SHA256,
		digest[:],
	)
	if err != nil {
		return "", fmt.Errorf("sign fixture ID token: %w", err)
	}
	token := input + "." + base64.RawURLEncoding.EncodeToString(signature)
	if mode == TokenBadSignature {
		return corruptSignature(token), nil
	}
	return token, nil
}

func corruptSignature(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) == 0 {
		return ""
	}
	signature[0] ^= 1
	return parts[0] + "." + parts[1] + "." +
		base64.RawURLEncoding.EncodeToString(signature)
}

func s256Challenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

type tokenResponse struct {
	AccessToken string          `json:"access_token"`
	TokenType   string          `json:"token_type"`
	IDToken     json.RawMessage `json:"id_token,omitempty"`
}

type jwtHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
}

type jwtClaims struct {
	Issuer        string   `json:"iss"`
	Audience      string   `json:"aud"`
	Subject       string   `json:"sub"`
	Nonce         string   `json:"nonce"`
	ExpiresAt     int64    `json:"exp"`
	Email         string   `json:"email"`
	EmailVerified bool     `json:"email_verified"`
	Groups        []string `json:"groups,omitempty"`
	AuthTime      *int64   `json:"auth_time,omitempty"`
}
