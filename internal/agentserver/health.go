package agentserver

import (
	"context"
	"fmt"

	"durpdeploy/internal/events"
)

func (s *Server) maintainHealth(ctx context.Context) error {
	if s.eventBus == nil {
		return nil
	}
	transitions, err := s.repository.AdvanceAgentHealth(ctx)
	if err != nil {
		return err
	}
	if len(transitions) == 0 {
		return nil
	}
	// ponytail: best-effort alert batches run outside claim maintenance;
	// a durable delivery queue is only needed if alert delivery must retry.
	go func() {
		for _, transition := range transitions {
			var typ events.Type
			switch transition.Health {
			case "stale":
				typ = events.AgentStale
			case "offline":
				typ = events.AgentOffline
			case "healthy":
				if transition.Previous == "stale" ||
					transition.Previous == "offline" {
					typ = events.AgentRecovered
				}
			}
			if typ == "" {
				continue
			}
			s.eventBus.Publish(ctx, events.Event{
				Type: typ,
				Message: fmt.Sprintf(
					"Agent %s (%s) is %s. Inspect /admin/agents/%s",
					transition.Agent.Name, transition.Agent.ID,
					transition.Health, transition.Agent.ID,
				),
			})
		}
	}()
	return nil
}
