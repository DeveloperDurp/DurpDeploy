// Package agentbootstrap serves the agent's short-lived initial pairing listener.
package agentbootstrap

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
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
	StateDir        string
	ListenAddr      string
	AgentVersion    agentproto.AgentVersion
	Now             func() time.Time
	PairingTTL      time.Duration
	AfterPairCommit func(http.ResponseWriter) bool
}

type Listener struct {
	server     *http.Server
	listener   net.Listener
	done       chan struct{}
	paired     chan struct{}
	expiryDone chan struct{}
	once       sync.Once
	pairedOnce sync.Once

	identity        agenttls.Identity
	agentVersion    agentproto.AgentVersion
	now             func() time.Time
	offer           agentproto.PairingOffer
	session         agentproto.PairingSession
	store           agentstate.Store
	afterPairCommit func(http.ResponseWriter) bool
}

func Start(config Config) (*Listener, error) {
	if config.StateDir == "" {
		return nil, errors.New("bootstrap state directory is required")
	}
	if config.ListenAddr == "" {
		config.ListenAddr = DefaultListenAddr
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	ttl := config.PairingTTL
	if ttl == 0 {
		ttl = pairingTTL
	}
	if ttl < 0 {
		return nil, errors.New("bootstrap pairing TTL must not be negative")
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
	expiresAt := config.Now().Add(ttl)
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
		listener:     networkListener,
		done:         make(chan struct{}),
		paired:       make(chan struct{}),
		expiryDone:   make(chan struct{}),
		identity:     identity,
		agentVersion: config.AgentVersion,
		now:          config.Now,
		offer: agentproto.PairingOffer{
			Code: code, AgentPin: agentPin, ExpiresAt: expiresAt,
		},
		session:         session,
		store:           agentstate.NewStore(config.StateDir),
		afterPairCommit: config.AfterPairCommit,
	}
	result.server = &http.Server{Handler: result.handler()}
	go result.serve()
	go func() {
		defer close(result.expiryDone)
		timer := time.NewTimer(time.Until(expiresAt))
		defer timer.Stop()
		select {
		case <-result.done:
			return
		case <-result.paired:
			return
		case <-timer.C:
		}
		if !result.session.Expire(time.Now()) {
			return
		}
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

func (listener *Listener) Offer() agentproto.PairingOffer {
	return listener.offer
}

func (listener *Listener) Done() <-chan struct{} { return listener.done }

func (listener *Listener) Paired() <-chan struct{} { return listener.paired }

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
	mux.HandleFunc(agentproto.ServerInitPath, listener.serverInit)
	return http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Cache-Control", "no-store")
			mux.ServeHTTP(writer, request)
		},
	)
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
