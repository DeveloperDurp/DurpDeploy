package agentserver

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

type pairingConnection interface {
	CertificateDER() []byte
	Post(context.Context, agentproto.PairRequest) (int, error)
	Close() error
}

type tlsPairingConnection struct {
	address string
	conn    *tls.Conn
	reader  *bufio.Reader
	der     []byte
}

func connectPairing(
	ctx context.Context,
	address string,
	identity agenttls.Identity,
) (pairingConnection, error) {
	config, err := agenttls.NewBootstrapClientConfig(address)
	if err != nil {
		return nil, err
	}
	config.Certificates = []tls.Certificate{identity.Certificate}
	parsed, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	host := parsed.Host
	if parsed.Port() == "" {
		host = net.JoinHostPort(parsed.Hostname(), "443")
	}
	dialer := &tls.Dialer{Config: config}
	connection, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("open agent pairing TLS connection: %w", err)
	}
	tlsConnection := connection.(*tls.Conn)
	state := tlsConnection.ConnectionState()
	if len(state.PeerCertificates) != 1 {
		_ = tlsConnection.Close()
		return nil, errors.New("agent pairing requires one certificate")
	}
	return &tlsPairingConnection{
		address: address, conn: tlsConnection,
		reader: bufio.NewReader(tlsConnection),
		der:    append([]byte(nil), state.PeerCertificates[0].Raw...),
	}, nil
}

func (connection *tlsPairingConnection) CertificateDER() []byte {
	return append([]byte(nil), connection.der...)
}

func (connection *tlsPairingConnection) Post(
	ctx context.Context,
	payload agentproto.PairRequest,
) (int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("encode agent pairing request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		connection.address+agentproto.ServerInitPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return 0, fmt.Errorf("create agent pairing request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.conn.SetDeadline(deadline); err != nil {
			return 0, fmt.Errorf("set agent pairing deadline: %w", err)
		}
	}
	if err := request.Write(connection.conn); err != nil {
		return 0, fmt.Errorf("send agent pairing request: %w", err)
	}
	response, err := http.ReadResponse(connection.reader, request)
	if err != nil {
		return 0, fmt.Errorf("read agent pairing response: %w", err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(
		response.Body,
		agentproto.MaxPairingMessageBytes,
	)); err != nil {
		return 0, fmt.Errorf("discard agent pairing response: %w", err)
	}
	return response.StatusCode, nil
}

func (connection *tlsPairingConnection) Close() error {
	return connection.conn.Close()
}
