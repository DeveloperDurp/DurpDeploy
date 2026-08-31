package agentclient

import (
	"crypto/tls"
	"fmt"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agentstate"
	"durpdeploy/internal/agenttls"
)

func (client *Client) tlsConfig() (*tls.Config, error) {
	config, err := agenttls.NewClientConfig(client.serverURL, client.pins)
	if err != nil {
		return nil, fmt.Errorf("configure agent TLS: %w", err)
	}
	config.Certificates = []tls.Certificate{client.identity.Certificate}
	config.VerifyConnection = func(connection tls.ConnectionState) error {
		client.mu.Lock()
		pins := append([]agenttls.Fingerprint(nil), client.pins...)
		client.mu.Unlock()
		verifier, err := agenttls.NewClientConfig(client.serverURL, pins)
		if err != nil {
			return err
		}
		return verifier.VerifyConnection(connection)
	}
	return config, nil
}

func (client *Client) promotePin(state *tls.ConnectionState) error {
	if state == nil || len(state.PeerCertificates) != 1 {
		return fmt.Errorf(
			"agent server did not provide exactly one certificate",
		)
	}
	connected := agenttls.FingerprintOf(state.PeerCertificates[0].Raw)
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.pins) != 2 || connected != client.pins[1] {
		return nil
	}
	pins, err := agenttls.PromotePendingPin(client.pins, connected)
	if err != nil {
		return fmt.Errorf("promote server pin: %w", err)
	}
	if err := client.persistPins(pins); err != nil {
		return err
	}
	client.pins = pins
	return nil
}

func (client *Client) stagePins(raw []agentproto.CertificateFingerprint) error {
	if len(raw) == 0 || len(raw) > 2 {
		return fmt.Errorf("server returned invalid pin count")
	}
	pins, err := parsePins(raw)
	if err != nil {
		return err
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(pins) == 1 && pins[0] == client.pins[0] {
		return nil
	}
	if len(pins) == 2 && pins[0] == client.pins[0] {
		if err := client.persistPins(pins); err != nil {
			return err
		}
		client.pins = pins
		client.http.CloseIdleConnections()
		return nil
	}
	if len(client.pins) == 2 && pins[0] == client.pins[1] {
		return nil
	}
	if len(client.pins) != 1 {
		return fmt.Errorf("server pin rotation is already staged")
	}
	if pins[0] == client.pins[0] {
		return nil
	}
	staged := []agenttls.Fingerprint{client.pins[0], pins[0]}
	if err := client.persistPins(staged); err != nil {
		return err
	}
	client.pins = staged
	client.http.CloseIdleConnections()
	return nil
}

func (client *Client) persistPins(pins []agenttls.Fingerprint) error {
	client.state.ServerPins = append([]agenttls.Fingerprint(nil), pins...)
	if err := agentstate.NewStore(client.stateDir).
		Save(client.state); err != nil {
		return fmt.Errorf("save paired state: %w", err)
	}
	return nil
}

func parsePins(
	raw []agentproto.CertificateFingerprint,
) ([]agenttls.Fingerprint, error) {
	pins := make([]agenttls.Fingerprint, 0, len(raw))
	for _, value := range raw {
		pin, err := agenttls.ParseFingerprint(string(value))
		if err != nil {
			return nil, fmt.Errorf("parse server fingerprint: %w", err)
		}
		for _, existing := range pins {
			if pin == existing {
				return nil, fmt.Errorf("server pins must be unique")
			}
		}
		pins = append(pins, pin)
	}
	return pins, nil
}
