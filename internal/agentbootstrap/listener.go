// Package agentbootstrap serves the agent's short-lived initial pairing listener.
package agentbootstrap

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agentstate"
	"durpdeploy/internal/agenttls"
)

const (
	DefaultListenAddr = ":10943"
	pairingTTL        = 10 * time.Minute
)

type Config struct {
	StateDir   string
	ListenAddr string
}

type Listener struct {
	server   *http.Server
	listener net.Listener
	done     chan struct{}
	once     sync.Once

	identity agenttls.Identity
	offer    agentproto.BootstrapResponse
	session  agentproto.PairingSession
	store    agentstate.Store
}

func Start(config Config) (*Listener, error) {
	if config.StateDir == "" {
		return nil, errors.New("bootstrap state directory is required")
	}
	if config.ListenAddr == "" {
		config.ListenAddr = DefaultListenAddr
	}
	identityURL, err := identityURL(config.ListenAddr)
	if err != nil {
		return nil, err
	}
	identity, err := agenttls.LoadOrCreate(config.StateDir, identityURL)
	if err != nil {
		return nil, fmt.Errorf("load bootstrap identity: %w", err)
	}
	code, err := newPairingCode()
	if err != nil {
		return nil, err
	}
	agentPin, err := agentproto.ParseSHA256Pin(identity.Fingerprint.String())
	if err != nil {
		return nil, fmt.Errorf("parse bootstrap agent pin: %w", err)
	}
	expiresAt := time.Now().Add(pairingTTL)
	session, err := agentproto.NewPairingSession(agentproto.PairingOffer{
		Code: code, AgentPin: agentPin, ExpiresAt: expiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create pairing session: %w", err)
	}
	networkListener, err := net.Listen("tcp", config.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen for pairing: %w", err)
	}
	result := &Listener{
		listener: networkListener,
		done:     make(chan struct{}),
		identity: identity,
		offer: agentproto.BootstrapResponse{
			ProtocolEnvelope: agentproto.ProtocolEnvelope{
				Protocol: agentproto.AgentV1,
			},
			PairingCode: code,
			AgentPin:    agentPin,
			ExpiresAt:   expiresAt,
		},
		session: session,
		store:   agentstate.NewStore(config.StateDir),
	}
	result.server = &http.Server{Handler: result.handler()}
	go result.serve()
	go func() {
		timer := time.NewTimer(time.Until(expiresAt))
		defer timer.Stop()
		<-timer.C
		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			time.Second,
		)
		defer cancel()
		_ = result.Shutdown(shutdownCtx)
	}()
	return result, nil
}

func (listener *Listener) Endpoint() string {
	return "https://" + listener.listener.Addr().String()
}

func (listener *Listener) Offer() agentproto.BootstrapResponse {
	return listener.offer
}

func (listener *Listener) Done() <-chan struct{} { return listener.done }

func (listener *Listener) Shutdown(ctx context.Context) error {
	var shutdownErr error
	listener.once.Do(func() { shutdownErr = listener.server.Shutdown(ctx) })
	select {
	case <-listener.done:
		return shutdownErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (listener *Listener) serve() {
	err := listener.server.Serve(tls.NewListener(listener.listener, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{listener.identity.Certificate},
		ClientAuth:   tls.RequestClientCert,
	}))
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		_ = listener.listener.Close()
	}
	close(listener.done)
}

func (listener *Listener) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(agentproto.BootstrapPath, listener.bootstrap)
	mux.HandleFunc(agentproto.PairPath, listener.pair)
	mux.HandleFunc(agentproto.PairCommitPath, listener.commitPair)
	return http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Cache-Control", "no-store")
			mux.ServeHTTP(writer, request)
		},
	)
}

func (listener *Listener) bootstrap(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodGet {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(writer).Encode(listener.offer); err != nil {
		return
	}
}

func (listener *Listener) pair(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	pairRequest, err := agentproto.DecodeRequest[agentproto.PairRequest](
		request.Body,
	)
	if err != nil || pairRequest.AgentID == "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if request.TLS == nil || len(request.TLS.PeerCertificates) != 1 {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	serverFingerprint := agenttls.FingerprintOf(
		request.TLS.PeerCertificates[0].Raw,
	)
	serverPin, err := agentproto.ParseSHA256Pin(serverFingerprint.String())
	if err != nil || serverPin != pairRequest.ServerPin {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	err = listener.session.Prepare(
		time.Now(),
		agentproto.PairingConfirmation{
			Code:          pairRequest.PairingCode,
			ObservedAgent: listener.offer.AgentPin,
			ServerPin:     serverPin,
			PullEndpoint:  pairRequest.PullEndpoint,
		},
	)
	if err != nil {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(writer).Encode(agentproto.PairResponse{
		ProtocolEnvelope: agentproto.ProtocolEnvelope{
			Protocol: agentproto.AgentV1,
		},
		AgentPin: listener.offer.AgentPin,
	}); err != nil {
		return
	}
}

func (listener *Listener) commitPair(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	pairRequest, err := agentproto.DecodeRequest[agentproto.PairRequest](request.Body)
	if err != nil || pairRequest.AgentID == "" || request.TLS == nil || len(request.TLS.PeerCertificates) != 1 {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	serverFingerprint := agenttls.FingerprintOf(request.TLS.PeerCertificates[0].Raw)
	serverPin, err := agentproto.ParseSHA256Pin(serverFingerprint.String())
	if err != nil || serverPin != pairRequest.ServerPin {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	_, err = listener.session.PairAndCommit(time.Now(), agentproto.PairingConfirmation{
		Code: pairRequest.PairingCode, ObservedAgent: listener.offer.AgentPin,
		ServerPin: serverPin, PullEndpoint: pairRequest.PullEndpoint,
	}, func() error {
		state, stateErr := agentstate.New(pairRequest.PullEndpoint.String(), []agenttls.Fingerprint{serverFingerprint}, pairRequest.AgentID)
		if stateErr != nil {
			return stateErr
		}
		return listener.store.Save(state)
	})
	if err != nil {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
	go func() {
		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			time.Second,
		)
		defer cancel()
		_ = listener.Shutdown(shutdownCtx)
	}()
}

func identityURL(address string) (string, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("parse bootstrap listen address: %w", err)
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "0.0.0.0" || host == "::" {
		host, err = os.Hostname()
		if err != nil {
			return "", fmt.Errorf("read bootstrap hostname: %w", err)
		}
	}
	return "https://" + host, nil
}

func newPairingCode() (agentproto.PairingCode, error) {
	bytes := make([]byte, agentproto.PairingCodeEntropyBytes)
	if _, err := rand.Read(bytes); err != nil {
		return agentproto.PairingCode{}, fmt.Errorf(
			"generate pairing code: %w",
			err,
		)
	}
	return agentproto.ParsePairingCode(
		base64.RawURLEncoding.EncodeToString(bytes),
	)
}
