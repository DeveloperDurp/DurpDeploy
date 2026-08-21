package agentclient

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"durpdeploy/internal/agentpayload"
	"durpdeploy/internal/agentproto"
)

// Poll waits for and returns one claimed deployment, or nil when no deployment is available.
func (client *Client) Poll(
	ctx context.Context,
) (*agentproto.PollResponse, error) {
	request := agentproto.PollRequest{
		ProtocolEnvelope: agentproto.ProtocolEnvelope{
			Protocol: client.protocol,
		},
		AgentVersion: client.agentVersion,
	}
	var response agentproto.PollResponse
	status, err := client.sendStatus(
		ctx,
		agentproto.PollPath,
		request,
		"",
		&response,
	)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent {
		return nil, nil
	}
	if status != http.StatusOK {
		return nil, &StatusError{Status: status}
	}
	if response.DeploymentID < 1 || response.Payload == "" ||
		response.ClaimToken == "" {
		return nil, fmt.Errorf("invalid poll response")
	}
	return &response, nil
}

// DecodePayload opens the claimed deployment envelope with this client's identity.
func (client *Client) DecodePayload(
	response agentproto.PollResponse,
) ([]byte, error) {
	return agentpayload.Open(
		client.identity,
		int64(response.DeploymentID),
		[]byte(response.Payload),
	)
}

// Heartbeat renews a claimed deployment and records any staged server pin.
func (client *Client) Heartbeat(
	ctx context.Context,
	id agentproto.DeploymentID,
	token agentproto.ClaimToken,
) (agentproto.HeartbeatResponse, error) {
	var response agentproto.HeartbeatResponse
	request := agentproto.HeartbeatRequest{
		ProtocolEnvelope: agentproto.ProtocolEnvelope{
			Protocol: client.protocol,
		},
		ClaimToken: token,
	}
	if err := client.send(
		ctx,
		deploymentPath(agentproto.HeartbeatPath, id),
		request,
		http.StatusOK,
		"",
		&response,
	); err != nil {
		return agentproto.HeartbeatResponse{}, err
	}
	if err := client.stagePins(response.ServerPins); err != nil {
		return agentproto.HeartbeatResponse{}, err
	}
	return response, nil
}

func (client *Client) Start(
	ctx context.Context,
	id agentproto.DeploymentID,
	token agentproto.ClaimToken,
) error {
	return client.lifecycle(
		ctx,
		agentproto.StartPath,
		id,
		agentproto.StartRequest{
			ProtocolEnvelope: agentproto.ProtocolEnvelope{
				Protocol: client.protocol,
			},
			ClaimToken: token,
		},
	)
}

func (client *Client) Logs(
	ctx context.Context,
	id agentproto.DeploymentID,
	request agentproto.LogBatchRequest,
) error {
	request.Protocol = client.protocol
	return client.lifecycle(ctx, agentproto.LogsPath, id, request)
}

func (client *Client) Result(
	ctx context.Context,
	id agentproto.DeploymentID,
	request agentproto.ResultRequest,
) error {
	request.Protocol = client.protocol
	return client.lifecycle(ctx, agentproto.ResultPath, id, request)
}

func (client *Client) Cancelled(
	ctx context.Context,
	id agentproto.DeploymentID,
	token agentproto.ClaimToken,
) error {
	return client.lifecycle(
		ctx,
		agentproto.CancelledPath,
		id,
		agentproto.CancelledRequest{
			ProtocolEnvelope: agentproto.ProtocolEnvelope{
				Protocol: client.protocol,
			},
			ClaimToken: token,
		},
	)
}

func (client *Client) lifecycle(
	ctx context.Context,
	path string,
	id agentproto.DeploymentID,
	request agentproto.Request,
) error {
	if id < 1 {
		return fmt.Errorf("deployment ID must be positive")
	}
	return client.send(
		ctx,
		deploymentPath(path, id),
		request,
		http.StatusNoContent,
		"",
		nil,
	)
}

func deploymentPath(pattern string, id agentproto.DeploymentID) string {
	return strings.Replace(pattern, "{id}", strconv.FormatInt(int64(id), 10), 1)
}
