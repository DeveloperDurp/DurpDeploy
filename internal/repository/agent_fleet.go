package repository

import (
	"context"
	"fmt"

	"durpdeploy/internal/db"
)

// AgentFleetReports reads list-page fields in a fixed number of DB queries.
func (r *Repository) AgentFleetReports(
	ctx context.Context,
	agents []db.Agent,
) (map[string]AgentHealthReport, map[string][]string, error) {
	now, err := r.Queries.CurrentUnixTime(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("fleet clock: %w", err)
	}
	baselines, err := r.Queries.ListFleetHealthBaselines(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("fleet baselines: %w", err)
	}
	started := make(map[string]int64, len(baselines))
	for _, row := range baselines {
		started[row.ID] = row.Baseline
	}
	version, recommended := fleetBuildVersions()
	reports := make(map[string]AgentHealthReport, len(agents))
	for _, agent := range agents {
		reports[agent.ID] = AgentHealthReport{
			Health:        AgentHealthAt(agent, now, started[agent.ID]),
			CurrentWork:   []db.ListAgentCurrentWorkRow{},
			ServerVersion: version, RecommendedAgentVersion: recommended,
			Compatibility: agentCompatibility(agent),
		}
	}
	current, err := r.Queries.ListFleetCurrentWork(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("fleet current work: %w", err)
	}
	for _, row := range current {
		report := reports[row.AgentID]
		report.CurrentWork = append(
			report.CurrentWork,
			db.ListAgentCurrentWorkRow{
				DeploymentID: row.DeploymentID,
				StepIndex:    row.StepIndex,
				State:        row.State,
			},
		)
		reports[row.AgentID] = report
	}
	for _, agent := range agents {
		report := reports[agent.ID]
		report.AdministrativeStatus = agentAdministrativeStatus(
			agent,
			len(report.CurrentWork),
		)
		reports[agent.ID] = report
	}
	queued, err := r.Queries.ListFleetQueuedWork(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("fleet queued work: %w", err)
	}
	for _, row := range queued {
		report := reports[row.AgentID]
		report.QueuedWork = row.QueuedWork
		reports[row.AgentID] = report
	}
	successes, err := r.Queries.ListFleetLastSuccessfulDeployments(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("fleet last successes: %w", err)
	}
	for _, row := range successes {
		report := reports[row.AgentID]
		report.LastSuccessfulDeployment = &db.GetAgentLastSuccessfulDeploymentRow{
			DeploymentID: row.DeploymentID,
			FinishedAt:   row.FinishedAt,
		}
		reports[row.AgentID] = report
	}
	failures, err := r.Queries.ListFleetLastErrors(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("fleet last errors: %w", err)
	}
	for _, row := range failures {
		report := reports[row.AgentID]
		report.LastError = &db.GetAgentLastErrorRow{
			DeploymentID: row.DeploymentID, StepIndex: row.StepIndex,
			State: row.State, Reason: row.Reason, FinishedAt: row.FinishedAt,
		}
		reports[row.AgentID] = report
	}
	capabilities, err := r.Queries.ListFleetInterpreters(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("fleet interpreters: %w", err)
	}
	interpreters := make(map[string][]string, len(agents))
	for _, row := range capabilities {
		interpreters[row.AgentID] = append(
			interpreters[row.AgentID], row.Interpreter,
		)
	}
	return reports, interpreters, nil
}
