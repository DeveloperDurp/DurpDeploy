package oidctest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
)

func (f *Fixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		f.serveDiscovery(w, r)
	case "/authorize":
		f.serveAuthorize(w, r)
	case "/token":
		f.serveToken(w, r)
	case "/keys":
		f.serveJWKS(w)
	default:
		http.NotFound(w, r)
	}
}

func (f *Fixture) serveDiscovery(w http.ResponseWriter, r *http.Request) {
	f.discoveryRequests.Add(1)
	if consumeFailure(&f.discoveryFailures) {
		http.Error(
			w,
			"fixture discovery failure",
			http.StatusServiceUnavailable,
		)
		return
	}
	if f.blockDiscovery.Load() {
		<-r.Context().Done()
		f.discoveryCancelOnce.Do(func() { close(f.discoveryCanceled) })
		return
	}
	issuer := f.URL()
	body, err := json.Marshal(discoveryResponse{
		Issuer:                issuer,
		AuthorizationEndpoint: issuer + "/authorize",
		TokenEndpoint:         issuer + "/token",
		JWKSURI:               issuer + "/keys",
		IDTokenSigningAlgs:    []string{"RS256"},
	})
	if err != nil {
		http.Error(
			w,
			"encode fixture discovery",
			http.StatusInternalServerError,
		)
		return
	}
	if err := writeOIDCJSON(w, body); err != nil {
		return
	}
}

func (f *Fixture) serveAuthorize(w http.ResponseWriter, r *http.Request) {
	f.authorizeRequests.Add(1)
	values := r.URL.Query()
	request := AuthorizationRequest{
		ClientID:            values.Get("client_id"),
		RedirectURL:         values.Get("redirect_uri"),
		ResponseType:        values.Get("response_type"),
		State:               values.Get("state"),
		Nonce:               values.Get("nonce"),
		Scope:               values.Get("scope"),
		CodeChallenge:       values.Get("code_challenge"),
		CodeChallengeMethod: values.Get("code_challenge_method"),
		Prompt:              values.Get("prompt"),
		MaxAge:              values.Get("max_age"),
	}
	if !f.validAuthorizationRequest(request) {
		http.Error(
			w,
			"invalid fixture authorization request",
			http.StatusBadRequest,
		)
		return
	}
	callback, err := url.Parse(request.RedirectURL)
	if err != nil || callback.Scheme == "" || callback.Host == "" {
		http.Error(w, "invalid fixture redirect URI", http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	request.Code = f.newCodeLocked(request.Nonce, request.CodeChallenge)
	f.capture.Authorization = request
	f.mu.Unlock()

	query := callback.Query()
	query.Set("code", request.Code)
	query.Set("state", request.State)
	callback.RawQuery = query.Encode()
	http.Redirect(w, r, callback.String(), http.StatusFound)
}

func (f *Fixture) validAuthorizationRequest(request AuthorizationRequest) bool {
	if request.ClientID != f.clientID || request.RedirectURL != f.callbackURL ||
		request.ResponseType != "code" ||
		request.CodeChallenge == "" || request.CodeChallengeMethod != "S256" ||
		request.State == "" || request.Nonce == "" {
		return false
	}
	if request.Scope == "" || !scopeContainsOpenID(request.Scope) {
		return false
	}
	return true
}

func scopeContainsOpenID(scope string) bool {
	for _, value := range strings.Fields(scope) {
		if value == "openid" {
			return true
		}
	}
	return false
}

func (f *Fixture) serveToken(w http.ResponseWriter, r *http.Request) {
	f.tokenRequests.Add(1)
	if consumeFailure(&f.tokenFailures) {
		http.Error(w, "fixture token failure", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid fixture token form", http.StatusBadRequest)
		return
	}
	clientID, clientSecret, basicAuth := r.BasicAuth()
	if !basicAuth || clientID != f.clientID || clientSecret != f.clientSecret {
		if err := writeOAuthError(
			w,
			"invalid_client",
			http.StatusUnauthorized,
		); err != nil {
			return
		}
		return
	}
	code := r.Form.Get("code")
	codeVerifier := r.Form.Get("code_verifier")
	nonce, valid := f.redeemCode(code, codeVerifier)
	if !valid {
		if err := writeOAuthError(
			w,
			"invalid_grant",
			http.StatusBadRequest,
		); err != nil {
			return
		}
		return
	}
	body, err := f.tokenResponse(nonce)
	if err != nil {
		http.Error(w, "encode fixture token", http.StatusInternalServerError)
		return
	}
	if err := writeOIDCJSON(w, body); err != nil {
		return
	}
}

func (f *Fixture) serveJWKS(w http.ResponseWriter) {
	f.jwksRequests.Add(1)
	f.mu.RLock()
	privateKey := f.privateKey
	keyID := f.keyID
	f.mu.RUnlock()
	body, err := json.Marshal(jwksResponse{Keys: []jwk{{
		KeyType: "RSA",
		KeyID:   keyID,
		Use:     "sig",
		Alg:     "RS256",
		N: base64.RawURLEncoding.EncodeToString(
			privateKey.PublicKey.N.Bytes(),
		),
		E: base64.RawURLEncoding.EncodeToString(
			big.NewInt(int64(privateKey.PublicKey.E)).Bytes(),
		),
	}}})
	if err != nil {
		http.Error(w, "encode fixture JWKS", http.StatusInternalServerError)
		return
	}
	if err := writeOIDCJSON(w, body); err != nil {
		return
	}
}

func (f *Fixture) redeemCode(code, codeVerifier string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.capture.Token = TokenRequest{Code: code, CodeVerifier: codeVerifier}
	stored, ok := f.codes[code]
	if !ok || stored.redeemed ||
		stored.codeChallenge != s256Challenge(codeVerifier) {
		return "", false
	}
	stored.redeemed = true
	f.codes[code] = stored
	return stored.nonce, true
}

func writeOAuthError(w http.ResponseWriter, code string, status int) error {
	body, err := json.Marshal(oauthError{Code: code})
	if err != nil {
		return fmt.Errorf("marshal fixture OAuth error: %w", err)
	}
	return writeOIDCJSONStatus(w, body, status)
}

func writeOIDCJSON(w http.ResponseWriter, body []byte) error {
	return writeOIDCJSONStatus(w, body, http.StatusOK)
}

func writeOIDCJSONStatus(
	w http.ResponseWriter,
	body []byte,
	status int,
) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("write fixture JSON: %w", err)
	}
	return nil
}

type discoveryResponse struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	IDTokenSigningAlgs    []string `json:"id_token_signing_alg_values_supported"`
}

type jwksResponse struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	KeyType string `json:"kty"`
	KeyID   string `json:"kid"`
	Use     string `json:"use"`
	Alg     string `json:"alg"`
	N       string `json:"n"`
	E       string `json:"e"`
}

type oauthError struct {
	Code string `json:"error"`
}
