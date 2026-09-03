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

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
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
	PublicIdentity string
	AgentPin       agentproto.SHA256Pin
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

func Pair(ctx context.Context, input PairInput) (Bootstrap, error) {
	return pair(ctx, input)
}

func pair(ctx context.Context, input PairInput) (Bootstrap, error) {
	baseURL, err := parseEndpoint(input.Endpoint)
	if err != nil {
		return Bootstrap{}, err
	}
	pin, err := agenttls.ParseFingerprint(input.AgentPin.String())
	if err != nil {
		return Bootstrap{}, fmt.Errorf("parse agent pin: %w", err)
	}
	config, err := agenttls.NewPairingBootstrapClientConfig(baseURL, pin)
	if err != nil {
		return Bootstrap{}, fmt.Errorf("pair TLS: %w", err)
	}
	config.Certificates = []tls.Certificate{input.Identity.Certificate}
	body, err := json.Marshal(input.Request)
	if err != nil {
		return Bootstrap{}, fmt.Errorf("encode pair request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		baseURL+agentproto.ServerInitPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return Bootstrap{}, fmt.Errorf("create pair request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{
		Timeout:   requestTimeout,
		Transport: &http.Transport{TLSClientConfig: config},
	}).Do(request)
	if err != nil {
		return Bootstrap{}, fmt.Errorf("pair agent: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return Bootstrap{}, fmt.Errorf(
			"pair agent returned HTTP %d",
			response.StatusCode,
		)
	}
	if response.TLS == nil || len(response.TLS.PeerCertificates) != 1 {
		return Bootstrap{}, fmt.Errorf(
			"server-init agent did not provide exactly one certificate",
		)
	}
	return Bootstrap{
		PublicIdentity: string(pem.EncodeToMemory(&pem.Block{
			Type: "CERTIFICATE", Bytes: response.TLS.PeerCertificates[0].Raw,
		})),
		AgentPin: input.AgentPin,
	}, nil
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
