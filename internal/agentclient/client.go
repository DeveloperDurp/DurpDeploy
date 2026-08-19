// Package agentclient provides the agent's outbound-only mTLS control-plane client.
package agentclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agenttls"
)

const (
	requestTimeout = 30 * time.Second
	maxBackoff     = 5 * time.Minute
)

var ErrEnrollmentUnavailable = errors.New(
	"agent enrollment token is unavailable",
)

// Config contains all persistent and in-memory client inputs. EnrollmentToken is
// used only for first-run enrollment and is never written to disk.
type Config struct {
	ServerURL         string
	ServerFingerprint string
	StateDir          string
	EnrollmentToken   string
	AgentID           agentproto.AgentID
	Name              string
	AgentVersion      agentproto.AgentVersion
	Protocol          string
}

// Client makes only outbound HTTP requests to the configured agent server.
type Client struct {
	serverURL       string
	stateDir        string
	enrollmentToken string
	agentID         agentproto.AgentID
	name            string
	agentVersion    agentproto.AgentVersion
	protocol        agentproto.ProtocolVersion
	identity        agenttls.Identity
	http            *http.Client

	mu       sync.Mutex
	pins     []agenttls.Fingerprint
	enrolled bool
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
	jitter   func(int64) (int64, error)
}

// New loads or creates the mTLS identity and pinned-server state.
func New(config Config) (*Client, error) {
	if config.StateDir == "" || config.ServerURL == "" ||
		config.ServerFingerprint == "" ||
		config.AgentID == "" ||
		config.Name == "" ||
		config.AgentVersion == "" {
		return nil, fmt.Errorf(
			"agent client requires server, state, and agent identity",
		)
	}
	protocol, err := agentproto.ParseProtocolVersion(config.Protocol)
	if err != nil {
		return nil, fmt.Errorf("parse protocol version: %w", err)
	}
	initialPin, err := agenttls.ParseFingerprint(config.ServerFingerprint)
	if err != nil {
		return nil, fmt.Errorf("parse server fingerprint: %w", err)
	}
	identity, err := agenttls.LoadOrCreate(config.StateDir, config.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("load agent identity: %w", err)
	}
	pins, err := loadPins(config.StateDir, initialPin)
	if err != nil {
		return nil, err
	}
	enrolled, err := isEnrolled(config.StateDir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(config.StateDir)
	if err != nil {
		return nil, fmt.Errorf("stat agent state directory: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf(
			"agent state directory must not be group or world accessible",
		)
	}
	client := &Client{
		serverURL:       config.ServerURL,
		stateDir:        config.StateDir,
		enrollmentToken: config.EnrollmentToken,
		agentID:         config.AgentID,
		name:            config.Name,
		agentVersion:    config.AgentVersion,
		protocol:        protocol,
		identity:        identity,
		pins:            pins,
		enrolled:        enrolled,
		now:             time.Now,
		sleep:           sleep,
		jitter:          randomInt,
	}
	tlsConfig, err := client.tlsConfig()
	if err != nil {
		return nil, err
	}
	client.http = &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			TLSClientConfig:     tlsConfig,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        4,
			MaxConnsPerHost:     2,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     30 * time.Second,
		},
	}
	return client, nil
}

// Close releases idle outbound connections. It never starts a listener.
func (client *Client) Close() { client.http.CloseIdleConnections() }

// StateDir returns the private directory holding this client's identity and pins.
func (client *Client) StateDir() string { return client.stateDir }

func (client *Client) send(
	ctx context.Context,
	path string,
	payload any,
	expected int,
	authorization string,
	response any,
) error {
	status, err := client.sendStatus(
		ctx,
		path,
		payload,
		authorization,
		response,
	)
	if err != nil {
		return err
	}
	if status != expected {
		return &StatusError{Status: status}
	}
	return nil
}

func (client *Client) sendStatus(
	ctx context.Context,
	path string,
	payload any,
	authorization string,
	output any,
) (int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("encode agent request: %w", err)
	}
	for attempt := 0; ; attempt++ {
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			client.serverURL+path,
			bytes.NewReader(body),
		)
		if err != nil {
			return 0, fmt.Errorf("create agent request: %w", err)
		}
		request.Header.Set("Content-Type", "application/json")
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		response, err := client.http.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			if err := client.wait(ctx, attempt, 0); err != nil {
				return 0, err
			}
			continue
		}
		if err := client.promotePin(response.TLS); err != nil {
			_ = response.Body.Close()
			return 0, err
		}
		if response.StatusCode == http.StatusServiceUnavailable ||
			response.StatusCode == http.StatusTooManyRequests {
			retryAfter := parseRetryAfter(
				response.Header.Get("Retry-After"),
				client.now(),
			)
			_ = response.Body.Close()
			if err := client.wait(ctx, attempt, retryAfter); err != nil {
				return 0, err
			}
			continue
		}
		defer response.Body.Close()
		if output != nil && response.StatusCode >= http.StatusOK &&
			response.StatusCode < http.StatusMultipleChoices &&
			response.StatusCode != http.StatusNoContent {
			if err := decodeResponse(response.Body, output); err != nil {
				return 0, err
			}
		}
		return response.StatusCode, nil
	}
}

// StatusError reports a non-retryable HTTP response without retaining its body.
type StatusError struct{ Status int }

func (err *StatusError) Error() string {
	return fmt.Sprintf("agent server returned HTTP %d", err.Status)
}

func (client *Client) wait(
	ctx context.Context,
	attempt int,
	retryAfter time.Duration,
) error {
	limit := time.Second
	for range attempt {
		if limit >= maxBackoff/2 {
			limit = maxBackoff
			break
		}
		limit *= 2
	}
	value, err := client.jitter(int64(limit))
	if err != nil {
		return fmt.Errorf("draw retry jitter: %w", err)
	}
	delay := time.Duration(value)
	if retryAfter > delay {
		delay = retryAfter
	}
	return client.sleep(ctx, delay)
}

func randomInt(limit int64) (int64, error) {
	if limit < 1 {
		return 0, nil
	}
	value, err := rand.Int(rand.Reader, big.NewInt(limit))
	if err != nil {
		return 0, err
	}
	return value.Int64(), nil
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseRetryAfter(raw string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(
		raw,
		10,
		64,
	); err == nil &&
		seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(raw)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func decodeResponse(body io.Reader, output any) error {
	payload, err := io.ReadAll(
		io.LimitReader(body, agentproto.MaxRequestBytes+1),
	)
	if err != nil {
		return fmt.Errorf("read agent response: %w", err)
	}
	if len(payload) > agentproto.MaxRequestBytes || !utf8.Valid(payload) {
		return fmt.Errorf("invalid agent response")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode agent response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode agent response: trailing data")
	}
	return nil
}
