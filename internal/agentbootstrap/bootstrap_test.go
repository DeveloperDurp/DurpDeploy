package agentbootstrap_test

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"durpdeploy/internal/agentbootstrap"
	"durpdeploy/internal/agentpairing"
	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agentstate"
	"durpdeploy/internal/agenttls"
)

func TestListener_persistsPinnedServerAndStops_when_pairingSucceeds(
	t *testing.T,
) {
	// Given
	stateDir := t.TempDir()
	listener, err := agentbootstrap.Start(agentbootstrap.Config{
		StateDir:   stateDir,
		ListenAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatalf("start bootstrap listener: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			time.Second,
		)
		defer cancel()
		if shutdownErr := listener.Shutdown(shutdownCtx); shutdownErr != nil {
			t.Errorf("shutdown bootstrap listener: %v", shutdownErr)
		}
	})
	serverIdentity, err := agenttls.LoadOrCreate(
		t.TempDir(),
		"https://127.0.0.1",
	)
	if err != nil {
		t.Fatalf("create server identity: %v", err)
	}
	endpoint := listener.Endpoint()
	bootstrap, err := agentpairing.FetchBootstrap(
		context.Background(),
		endpoint,
	)
	if err != nil {
		t.Fatalf("fetch bootstrap: %v", err)
	}
	pullEndpoint, err := agentproto.ParsePullEndpoint("https://127.0.0.1:10944")
	if err != nil {
		t.Fatalf("parse pull endpoint: %v", err)
	}

	// When
	input := agentpairing.PairInput{
		Endpoint: endpoint,
		AgentPin: bootstrap.AgentPin,
		Identity: serverIdentity,
		Request: agentproto.PairRequest{
			ProtocolEnvelope: agentproto.ProtocolEnvelope{
				Protocol: agentproto.AgentV1,
			},
			PairingCode:  bootstrap.PairingCode,
			ServerPin:    mustPin(t, serverIdentity.Fingerprint.String()),
			PullEndpoint: pullEndpoint,
			AgentID:      "paired-agent",
		},
	}
	pair, err := agentpairing.Pair(context.Background(), input)
	if err == nil {
		err = agentpairing.Commit(context.Background(), input)
	}

	// Then
	if err != nil {
		t.Fatalf("pair bootstrap listener: %v", err)
	}
	if pair.AgentPin != bootstrap.AgentPin {
		t.Fatalf(
			"pair response pin = %s, want %s",
			pair.AgentPin,
			bootstrap.AgentPin,
		)
	}
	state, err := agentstate.NewStore(stateDir).Load()
	if err != nil {
		t.Fatalf("load paired state: %v", err)
	}
	if state.ServerURL != pullEndpoint.String() ||
		state.AgentID != "paired-agent" ||
		len(state.ServerPins) != 1 ||
		state.ServerPins[0] != serverIdentity.Fingerprint {
		t.Fatalf("paired state = %#v", state)
	}
	select {
	case <-listener.Done():
	case <-time.After(time.Second):
		t.Fatal("bootstrap listener remained open after durable pairing")
	}
}

func TestListener_keepsPairingPending_when_codeOrServerPinIsWrong(
	t *testing.T,
) {
	// Given
	stateDir := t.TempDir()
	listener, err := agentbootstrap.Start(agentbootstrap.Config{
		StateDir: stateDir, ListenAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatalf("start bootstrap listener: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			time.Second,
		)
		defer cancel()
		if shutdownErr := listener.Shutdown(shutdownCtx); shutdownErr != nil {
			t.Errorf("shutdown bootstrap listener: %v", shutdownErr)
		}
	})
	serverIdentity, err := agenttls.LoadOrCreate(
		t.TempDir(),
		"https://127.0.0.1",
	)
	if err != nil {
		t.Fatalf("create server identity: %v", err)
	}
	otherIdentity, err := agenttls.LoadOrCreate(
		t.TempDir(),
		"https://127.0.0.1",
	)
	if err != nil {
		t.Fatalf("create comparison identity: %v", err)
	}
	bootstrap, err := agentpairing.FetchBootstrap(
		context.Background(),
		listener.Endpoint(),
	)
	if err != nil {
		t.Fatalf("fetch bootstrap: %v", err)
	}
	pullEndpoint, err := agentproto.ParsePullEndpoint("https://127.0.0.1:10944")
	if err != nil {
		t.Fatalf("parse pull endpoint: %v", err)
	}
	wrongCode, err := agentproto.ParsePairingCode(
		base64.RawURLEncoding.EncodeToString(
			make([]byte, agentproto.PairingCodeEntropyBytes),
		),
	)
	if err != nil {
		t.Fatalf("parse wrong pairing code: %v", err)
	}

	// When
	_, wrongCodeErr := agentpairing.Pair(
		context.Background(),
		agentpairing.PairInput{
			Endpoint: listener.Endpoint(), AgentPin: bootstrap.AgentPin,
			Identity: serverIdentity,
			Request: agentproto.PairRequest{
				ProtocolEnvelope: agentproto.ProtocolEnvelope{
					Protocol: agentproto.AgentV1,
				},
				PairingCode: wrongCode, ServerPin: mustPin(t, serverIdentity.Fingerprint.String()),
				PullEndpoint: pullEndpoint, AgentID: "paired-agent",
			},
		},
	)
	_, wrongPinErr := agentpairing.Pair(
		context.Background(),
		agentpairing.PairInput{
			Endpoint: listener.Endpoint(), AgentPin: bootstrap.AgentPin,
			Identity: serverIdentity,
			Request: agentproto.PairRequest{
				ProtocolEnvelope: agentproto.ProtocolEnvelope{
					Protocol: agentproto.AgentV1,
				},
				PairingCode:  bootstrap.PairingCode,
				ServerPin:    mustPin(t, otherIdentity.Fingerprint.String()),
				PullEndpoint: pullEndpoint, AgentID: "paired-agent",
			},
		},
	)

	// Then
	if wrongCodeErr == nil || wrongPinErr == nil {
		t.Fatal("invalid pairing input was accepted")
	}
	if _, err := agentstate.NewStore(stateDir).
		Load(); !errors.Is(
		err,
		agentstate.ErrRePairRequired,
	) {
		t.Fatalf(
			"state after rejected pairing = %v, want re-pair required",
			err,
		)
	}
	if _, err := agentpairing.FetchBootstrap(
		context.Background(),
		listener.Endpoint(),
	); err != nil {
		t.Fatalf("bootstrap listener closed after rejected pairing: %v", err)
	}
}

func mustPin(t *testing.T, raw string) agentproto.SHA256Pin {
	t.Helper()
	pin, err := agentproto.ParseSHA256Pin(raw)
	if err != nil {
		t.Fatalf("parse pin: %v", err)
	}
	return pin
}
