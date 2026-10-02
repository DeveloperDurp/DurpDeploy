package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime/debug"

	"durpdeploy/internal/db"
)

type AgentHealthReport struct {
	Health                   string                                  `json:"health"`
	CurrentWork              []db.ListAgentCurrentWorkRow            `json:"current_work"`
	QueuedWork               int64                                   `json:"queued_work"`
	LastSuccessfulDeployment *db.GetAgentLastSuccessfulDeploymentRow `json:"last_successful_deployment"`
	LastError                *db.GetAgentLastErrorRow                `json:"last_error"`
	ServerVersion            string                                  `json:"server_version"`
	RecommendedAgentVersion  string                                  `json:"recommended_agent_version"`
	Compatibility            string                                  `json:"compatibility"`
}

// AgentHealthAt shares the fleet thresholds between API, UI, and alerts.
func AgentHealthAt(agent db.Agent, now int64) string {
	if agent.Status != "active" {
		return "unknown"
	}
	last := agent.CreatedAt
	if agent.LastHeartbeatAt.Valid {
		last = agent.LastHeartbeatAt.Int64
	}
	if now-last >= 600 {
		return "offline"
	}
	if now-last >= 120 {
		return "stale"
	}
	if !agent.LastHeartbeatAt.Valid {
		return "unknown"
	}
	return "healthy"
}

func (r *Repository) AgentHealthReport(
	ctx context.Context,
	agent db.Agent,
) (AgentHealthReport, error) {
	var report AgentHealthReport
	now, err := r.Queries.CurrentUnixTime(ctx)
	if err != nil {
		return report, fmt.Errorf("agent health clock: %w", err)
	}
	report.Health = AgentHealthAt(agent, now)
	report.CurrentWork, err = r.Queries.ListAgentCurrentWork(ctx, agent.ID)
	if err != nil {
		return report, fmt.Errorf("current agent work: %w", err)
	}
	if report.CurrentWork == nil {
		report.CurrentWork = []db.ListAgentCurrentWorkRow{}
	}
	report.QueuedWork, err = r.Queries.CountAgentQueuedWork(ctx, agent.ID)
	if err != nil {
		return report, fmt.Errorf("queued agent work: %w", err)
	}
	success, err := r.Queries.GetAgentLastSuccessfulDeployment(ctx, agent.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return report, fmt.Errorf("last agent success: %w", err)
	}
	if err == nil {
		report.LastSuccessfulDeployment = &success
	}
	failure, err := r.Queries.GetAgentLastError(ctx, agent.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return report, fmt.Errorf("last agent error: %w", err)
	}
	if err == nil {
		report.LastError = &failure
	}
	report.ServerVersion, report.RecommendedAgentVersion = fleetBuildVersions()
	report.Compatibility = "unknown"
	switch agent.AgentProtocol.String {
	case "agent/1":
		report.Compatibility = "Supported legacy protocol (Bash only); version unverified"
	case "agent/2":
		report.Compatibility = "Supported protocol; version unverified"
	}
	return report, nil
}

func fleetBuildVersions() (string, string) {
	version, recommended := "dev", "unknown"
	if build, ok := debug.ReadBuildInfo(); ok {
		if build.Main.Version != "" && build.Main.Version != "(devel)" {
			version = build.Main.Version
		}
		for _, setting := range build.Settings {
			if setting.Key == "vcs.revision" && version == "dev" {
				version += " (" + setting.Value + ")"
			}
		}
		for _, dependency := range build.Deps {
			if dependency.Path == "github.com/DeveloperDurp/durpdeploy-agent" {
				recommended = dependency.Version
			}
		}
	}
	return version, recommended
}

type AgentHealthTransition struct {
	Agent    db.Agent
	Previous string
	Health   string
}

func (r *Repository) AdvanceAgentHealth(
	ctx context.Context,
) ([]AgentHealthTransition, error) {
	var transitions []AgentHealthTransition
	err := withSQLiteBusyRetry(ctx, func() error {
		transitions = nil
		return r.WithTx(ctx, func(q *db.Queries) error {
			agents, err := q.ListAgents(ctx)
			if err != nil {
				return err
			}
			now, err := q.CurrentUnixTime(ctx)
			if err != nil {
				return err
			}
			for _, candidate := range agents {
				if candidate.Status != "active" {
					continue
				}
				locked, err := q.LockClaimAgent(ctx, candidate.ID)
				if err != nil {
					return err
				}
				if locked == 0 {
					continue
				}
				agent, err := q.GetAgent(ctx, candidate.ID)
				if err != nil {
					return err
				}
				health := AgentHealthAt(agent, now)
				changed, err := q.SetAgentHealthState(
					ctx,
					db.SetAgentHealthStateParams{
						ID: agent.ID, HealthState: health,
					},
				)
				if err != nil {
					return err
				}
				if changed == 1 {
					transitions = append(transitions, AgentHealthTransition{
						Agent:    agent,
						Previous: agent.HealthState,
						Health:   health,
					})
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("advance agent health: %w", err)
	}
	return transitions, nil
}
