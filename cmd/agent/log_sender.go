package main

import (
	"context"
	"sync"

	"durpdeploy/internal/agentclient"
	"durpdeploy/internal/agentproto"
)

type logSender struct {
	client *agentclient.Client
	claim  agentproto.PollResponse
	mu     sync.Mutex
	next   agentproto.LogSequence
	events []agentproto.LogEvent
}

func newLogSender(
	client *agentclient.Client,
	claim agentproto.PollResponse,
) *logSender {
	return &logSender{client: client, claim: claim, next: 1}
}

func (sender *logSender) Write(line string) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.events = append(
		sender.events,
		agentproto.LogEvent{Sequence: sender.next, Line: line},
	)
	sender.next++
	if len(sender.events) < agentproto.MaxLogEvents {
		return nil
	}
	return sender.flushLocked(context.Background())
}

func (sender *logSender) Flush(ctx context.Context) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return sender.flushLocked(ctx)
}

func (sender *logSender) flushLocked(ctx context.Context) error {
	if len(sender.events) == 0 {
		return nil
	}
	if err := sender.client.Logs(
		ctx,
		sender.claim.DeploymentID,
		agentproto.LogBatchRequest{
			ClaimToken: sender.claim.ClaimToken, Events: sender.events,
		},
	); err != nil {
		return err
	}
	sender.events = nil
	return nil
}
