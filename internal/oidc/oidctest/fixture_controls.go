package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"sync/atomic"
)

// NewCode issues an unused authorization code bound to the default nonce.
func (f *Fixture) NewCode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.newCodeLocked(defaultNonce, s256Challenge(defaultPKCEVerifier))
}

// SetClaims replaces the claims issued in future tokens.
func (f *Fixture) SetClaims(claims Claims) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims = cloneClaims(claims)
}

// SetTokenMode changes the response mode for future token requests.
func (f *Fixture) SetTokenMode(mode TokenMode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenMode = mode
}

// SetDiscoveryFailures makes the next count discovery requests fail.
func (f *Fixture) SetDiscoveryFailures(count int32) {
	f.discoveryFailures.Store(count)
}

// SetTokenFailures makes the next count token requests fail.
func (f *Fixture) SetTokenFailures(count int32) {
	f.tokenFailures.Store(count)
}

// BlockDiscovery waits for client cancellation and returns its notification.
func (f *Fixture) BlockDiscovery() <-chan struct{} {
	f.blockDiscovery.Store(true)
	return f.discoveryCanceled
}

// RotateSigningKey generates and serves a new key with a new key ID.
func (f *Fixture) RotateSigningKey() error {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate rotated fixture signing key: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keyVersion++
	f.privateKey = privateKey
	f.keyID = fmt.Sprintf("fixture-key-%d", f.keyVersion)
	return nil
}

// Counters returns a consistent snapshot of endpoint request counts.
func (f *Fixture) Counters() Counters {
	return Counters{
		Discovery: f.discoveryRequests.Load(),
		Authorize: f.authorizeRequests.Load(),
		Token:     f.tokenRequests.Load(),
		JWKS:      f.jwksRequests.Load(),
	}
}

// Capture returns the latest authorization and token metadata.
func (f *Fixture) Capture() Capture {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.capture
}

func (f *Fixture) newCodeLocked(nonce, codeChallenge string) string {
	f.nextCode++
	code := fmt.Sprintf("fixture-code-%d", f.nextCode)
	f.codes[code] = authorizationCode{
		nonce:         nonce,
		codeChallenge: codeChallenge,
	}
	return code
}

func claimsAreZero(claims Claims) bool {
	return claims.Subject == "" && claims.Email == "" &&
		!claims.EmailVerified &&
		len(claims.Groups) == 0 && claims.AuthTime.IsZero()
}

func cloneClaims(claims Claims) Claims {
	claims.Groups = append([]string(nil), claims.Groups...)
	return claims
}

func consumeFailure(counter *atomic.Int32) bool {
	for {
		remaining := counter.Load()
		if remaining <= 0 {
			return false
		}
		if counter.CompareAndSwap(remaining, remaining-1) {
			return true
		}
	}
}
