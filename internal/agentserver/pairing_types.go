package agentserver

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

var (
	ErrPairingConflict    = errors.New("pairing conflicts with durable state")
	ErrPairingFingerprint = errors.New(
		"observed agent fingerprint does not match",
	)
	ErrPairingUnavailable = errors.New("agent pairing is unavailable")
	ErrPairingChallenge   = errors.New("pairing confirmation is unavailable")
)

const PairingStatePaired = "paired"

type PairingInput struct {
	name            string
	address         string
	code            agentproto.PairingCode
	fingerprint     agenttls.Fingerprint
	expectedAgentID string
}

type PairingStartInput struct {
	name    string
	address string
	code    agentproto.PairingCode
}

type PairingChallenge struct {
	ID          string
	Name        string
	Address     string
	Fingerprint string
	ExpiresAt   time.Time
}

func ParsePairingStartInput(
	name string,
	address string,
	code string,
) (PairingStartInput, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 {
		return PairingStartInput{}, errors.New(
			"agent name must be between 1 and 255 characters",
		)
	}
	parsedAddress, err := parsePairingAddress(address)
	if err != nil {
		return PairingStartInput{}, err
	}
	parsedCode, err := agentproto.ParsePairingCode(code)
	if err != nil {
		return PairingStartInput{}, errors.New("invalid pairing code")
	}
	return PairingStartInput{
		name: name, address: parsedAddress, code: parsedCode,
	}, nil
}

func ParsePairingInput(
	address string,
	code string,
	fingerprint string,
) (PairingInput, error) {
	parsedAddress, err := parsePairingAddress(address)
	if err != nil {
		return PairingInput{}, err
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
		address: parsedAddress, code: parsedCode,
		fingerprint: parsedFingerprint,
	}, nil
}

func parsePairingAddress(address string) (string, error) {
	address = strings.TrimSpace(address)
	if !strings.Contains(address, "://") {
		address = "https://" + address
	}
	parsedAddress, err := url.Parse(address)
	if err != nil || parsedAddress.Scheme != "https" ||
		parsedAddress.Hostname() == "" || parsedAddress.User != nil ||
		parsedAddress.RawQuery != "" || parsedAddress.ForceQuery ||
		parsedAddress.Fragment != "" ||
		(parsedAddress.Path != "" && parsedAddress.Path != "/") {
		return "", errors.New(
			"agent address must be an HTTPS origin",
		)
	}
	return parsedAddress.String(), nil
}

func (input PairingInput) Address() string { return input.address }

func (input PairingInput) ForAgent(agentID string) PairingInput {
	input.expectedAgentID = agentID
	return input
}

func (input PairingInput) ExpectedAgentID() string {
	return input.expectedAgentID
}

type PairingResult struct {
	AgentID string `json:"agent_id"`
	State   string `json:"state"`
}

type Pairer interface {
	Pair(context.Context, PairingInput) (PairingResult, error)
}

type PairingManager interface {
	Pairer
	Begin(context.Context, PairingStartInput) (PairingChallenge, error)
	Challenge(string) (PairingChallenge, error)
	Approve(context.Context, string) (PairingResult, error)
	Deny(string) error
}
