package agentserver

import (
	"context"
	"errors"
	"net/url"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

var (
	ErrPairingConflict    = errors.New("pairing conflicts with durable state")
	ErrPairingFingerprint = errors.New(
		"observed agent fingerprint does not match",
	)
	ErrPairingUnavailable = errors.New("agent pairing is unavailable")
)

const PairingStatePaired = "paired"

type PairingInput struct {
	address     string
	code        agentproto.PairingCode
	fingerprint agenttls.Fingerprint
}

func ParsePairingInput(
	address string,
	code string,
	fingerprint string,
) (PairingInput, error) {
	parsedAddress, err := url.Parse(address)
	if err != nil || parsedAddress.Scheme != "https" ||
		parsedAddress.Hostname() == "" || parsedAddress.User != nil ||
		parsedAddress.RawQuery != "" || parsedAddress.ForceQuery ||
		parsedAddress.Fragment != "" ||
		(parsedAddress.Path != "" && parsedAddress.Path != "/") {
		return PairingInput{}, errors.New(
			"agent address must be an HTTPS origin",
		)
	}
	parsedCode, err := agentproto.ParsePairingCode(code)
	if err != nil {
		return PairingInput{}, errors.New("invalid pairing code")
	}
	if _, err := agentproto.ParseSHA256Pin(fingerprint); err != nil {
		return PairingInput{}, errors.New("invalid agent fingerprint")
	}
	parsedFingerprint, err := agenttls.ParseFingerprint(fingerprint)
	if err != nil {
		return PairingInput{}, errors.New("invalid agent fingerprint")
	}
	return PairingInput{
		address: parsedAddress.String(), code: parsedCode,
		fingerprint: parsedFingerprint,
	}, nil
}

func (input PairingInput) Address() string { return input.address }

type PairingResult struct {
	AgentID string `json:"agent_id"`
	State   string `json:"state"`
}

type Pairer interface {
	Pair(context.Context, PairingInput) (PairingResult, error)
}
