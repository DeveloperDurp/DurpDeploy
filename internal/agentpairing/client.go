// Package agentpairing performs the one-time, pinned agent pairing exchange.
package agentpairing

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agenttls"
)

const requestTimeout = 15 * time.Second

type PairInput struct {
	Endpoint string
	AgentPin agentproto.SHA256Pin
	Identity agenttls.Identity
	Request  agentproto.PairRequest
}

type Server struct {
	PullEndpoint agentproto.PullEndpoint
	Identity     agenttls.Identity
}

type Bootstrap struct {
	Offer          agentproto.BootstrapResponse
	PublicIdentity string
}

func NewServer(
	pullEndpoint agentproto.PullEndpoint,
	identity agenttls.Identity,
) (*Server, error) {
	if pullEndpoint.String() == "" ||
		len(identity.Certificate.Certificate) != 1 {
		return nil, fmt.Errorf(
			"pairing server requires pull endpoint and identity",
		)
	}
	return &Server{PullEndpoint: pullEndpoint, Identity: identity}, nil
}

func FetchBootstrap(
	ctx context.Context,
	endpoint string,
) (agentproto.BootstrapResponse, error) {
	bootstrap, err := FetchBootstrapIdentity(ctx, endpoint)
	if err != nil {
		return agentproto.BootstrapResponse{}, err
	}
	return bootstrap.Offer, nil
}

func FetchBootstrapIdentity(
	ctx context.Context,
	endpoint string,
) (Bootstrap, error) {
	baseURL, err := parseEndpoint(endpoint)
	if err != nil {
		return Bootstrap{}, err
	}
	config, err := agenttls.NewBootstrapClientConfig(baseURL)
	if err != nil {
		return Bootstrap{}, fmt.Errorf("bootstrap TLS: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		baseURL+agentproto.BootstrapPath,
		nil,
	)
	if err != nil {
		return Bootstrap{}, fmt.Errorf("create bootstrap request: %w", err)
	}
	response, err := (&http.Client{
		Timeout:   requestTimeout,
		Transport: &http.Transport{TLSClientConfig: config},
	}).Do(request)
	if err != nil {
		return Bootstrap{}, fmt.Errorf("fetch bootstrap: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Bootstrap{}, fmt.Errorf(
			"bootstrap agent returned HTTP %d",
			response.StatusCode,
		)
	}
	bootstrap, err := agentproto.DecodeBootstrapResponse(response.Body)
	if err != nil {
		return Bootstrap{}, fmt.Errorf("decode bootstrap: %w", err)
	}
	if err := matchesConnectionPin(
		response.TLS,
		bootstrap.AgentPin,
	); err != nil {
		return Bootstrap{}, err
	}
	return Bootstrap{
		Offer: bootstrap,
		PublicIdentity: string(pem.EncodeToMemory(&pem.Block{
			Type: "CERTIFICATE", Bytes: response.TLS.PeerCertificates[0].Raw,
		})),
	}, nil
}

func Pair(
	ctx context.Context,
	input PairInput,
) (agentproto.PairResponse, error) {
	return pair(ctx, input, agentproto.PairPath, http.StatusOK)
}

func Commit(
	ctx context.Context,
	input PairInput,
) error {
	_, err := pair(ctx, input, agentproto.PairCommitPath, http.StatusNoContent)
	return err
}

func pair(
	ctx context.Context,
	input PairInput,
	path string,
	expectedStatus int,
) (agentproto.PairResponse, error) {
	baseURL, err := parseEndpoint(input.Endpoint)
	if err != nil {
		return agentproto.PairResponse{}, err
	}
	pin, err := agenttls.ParseFingerprint(input.AgentPin.String())
	if err != nil {
		return agentproto.PairResponse{}, fmt.Errorf("parse agent pin: %w", err)
	}
	config, err := agenttls.NewClientConfig(
		baseURL,
		[]agenttls.Fingerprint{pin},
	)
	if err != nil {
		return agentproto.PairResponse{}, fmt.Errorf("pair TLS: %w", err)
	}
	config.Certificates = []tls.Certificate{input.Identity.Certificate}
	body, err := json.Marshal(input.Request)
	if err != nil {
		return agentproto.PairResponse{}, fmt.Errorf(
			"encode pair request: %w",
			err,
		)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		baseURL+path,
		bytes.NewReader(body),
	)
	if err != nil {
		return agentproto.PairResponse{}, fmt.Errorf(
			"create pair request: %w",
			err,
		)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{
		Timeout:   requestTimeout,
		Transport: &http.Transport{TLSClientConfig: config},
	}).Do(request)
	if err != nil {
		return agentproto.PairResponse{}, fmt.Errorf("pair agent: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		return agentproto.PairResponse{}, fmt.Errorf(
			"pair agent returned HTTP %d",
			response.StatusCode,
		)
	}
	if expectedStatus == http.StatusNoContent {
		return agentproto.PairResponse{}, nil
	}
	pair, err := agentproto.DecodePairResponse(response.Body)
	if err != nil {
		return agentproto.PairResponse{}, fmt.Errorf(
			"decode pair response: %w",
			err,
		)
	}
	if pair.AgentPin != input.AgentPin {
		return agentproto.PairResponse{}, fmt.Errorf(
			"agent pairing response pin mismatch",
		)
	}
	return pair, nil
}

func parseEndpoint(raw string) (string, error) {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("agent endpoint must be an HTTPS origin")
	}
	return parsed.String(), nil
}

func matchesConnectionPin(
	state *tls.ConnectionState,
	pin agentproto.SHA256Pin,
) error {
	if state == nil || len(state.PeerCertificates) != 1 {
		return fmt.Errorf(
			"bootstrap agent did not provide exactly one certificate",
		)
	}
	if agenttls.FingerprintOf(state.PeerCertificates[0].Raw).
		String() !=
		pin.String() {
		return fmt.Errorf("bootstrap response pin does not match TLS peer")
	}
	return nil
}
