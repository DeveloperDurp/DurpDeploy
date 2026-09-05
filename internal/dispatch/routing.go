package dispatch

import (
	"context"
	"database/sql"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"errors"
	"fmt"
)

var (
	ErrInvalidPolicy    = errors.New("invalid routing policy")
	ErrLabelNotFound    = errors.New("agent label not found")
	ErrNoEligibleAgents = errors.New("agent label has no eligible agents")
)

type Source string

const (
	SourceLegacy   Source = "legacy"
	SourceProject  Source = "project"
	SourceRequest  Source = "request"
	SourceSchedule Source = "schedule"
	SourceRetry    Source = "retry"
	SourceRedeploy Source = "redeploy"
)

type TargetMode string

const (
	TargetLocal  TargetMode = "local"
	TargetLabel  TargetMode = "label"
	TargetRemote TargetMode = "remote"
)

type Strategy string

const (
	StrategyRoundRobin Strategy = "round_robin"
	StrategyAll        Strategy = "all"
)

type Input struct {
	Source   Source
	Mode     string
	LabelID  int64
	Strategy string
}

type ParsedInput struct {
	Source   Source
	Mode     string
	LabelID  int64
	Strategy Strategy
}

type Policy struct {
	Source        Source
	Mode          TargetMode
	LabelID       int64
	LabelName     string
	Strategy      Strategy
	LegacyAgentID string
}

type Agent struct {
	ID   string
	Name string
}

type Resolver struct {
	repo *repository.Repository
}

func NewResolver(repo *repository.Repository) *Resolver {
	return &Resolver{repo: repo}
}

func ParseInput(input Input) (ParsedInput, error) {
	deferMode := ""
	switch input.Source {
	case SourceRequest, SourceRetry:
		deferMode = "default"
	case SourceSchedule:
		deferMode = "inherit"
	case SourceRedeploy:
		deferMode = "default"
	default:
		return ParsedInput{}, fmt.Errorf(
			"%w: source %q",
			ErrInvalidPolicy,
			input.Source,
		)
	}
	if input.Mode == deferMode && input.LabelID == 0 && input.Strategy == "" {
		return ParsedInput{Source: input.Source, Mode: input.Mode}, nil
	}
	if input.Mode == string(TargetLocal) && input.LabelID == 0 &&
		input.Strategy == "" {
		return ParsedInput{Source: input.Source, Mode: input.Mode}, nil
	}
	if input.Mode != string(TargetLabel) || input.LabelID <= 0 {
		return ParsedInput{}, fmt.Errorf(
			"%w: mode %q",
			ErrInvalidPolicy,
			input.Mode,
		)
	}
	strategy := Strategy(input.Strategy)
	if strategy != StrategyRoundRobin && strategy != StrategyAll {
		return ParsedInput{}, fmt.Errorf(
			"%w: strategy %q", ErrInvalidPolicy, input.Strategy,
		)
	}
	return ParsedInput{
		Source: input.Source, Mode: input.Mode,
		LabelID: input.LabelID, Strategy: strategy,
	}, nil
}

func (r *Resolver) Resolve(
	ctx context.Context,
	projectID int64,
	environmentID int64,
	input Input,
) (Policy, error) {
	parsed, err := ParseInput(input)
	if err != nil {
		return Policy{}, err
	}
	if parsed.Mode == string(TargetLocal) {
		return Policy{Source: parsed.Source, Mode: TargetLocal}, nil
	}
	if parsed.Mode == string(TargetLabel) {
		return r.labelPolicy(
			ctx,
			parsed.Source,
			parsed.LabelID,
			parsed.Strategy,
		)
	}
	stored, err := r.repo.Queries.GetProjectExecutionPolicy(ctx, projectID)
	if err == nil {
		return r.storedPolicy(ctx, stored)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Policy{}, fmt.Errorf("get project routing policy: %w", err)
	}
	assignment, err := r.repo.Queries.GetEnvironmentAgentAssignment(
		ctx,
		environmentID,
	)
	if err == nil {
		return Policy{
			Source: SourceLegacy, Mode: TargetRemote,
			LegacyAgentID: assignment.AgentID,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Policy{}, fmt.Errorf(
			"get legacy environment assignment: %w",
			err,
		)
	}
	return Policy{Source: SourceLegacy, Mode: TargetLocal}, nil
}

func (r *Resolver) storedPolicy(
	ctx context.Context,
	stored db.ProjectExecutionPolicy,
) (Policy, error) {
	if stored.TargetMode == string(TargetLocal) &&
		!stored.AgentLabelID.Valid && !stored.AgentStrategy.Valid {
		return Policy{Source: SourceProject, Mode: TargetLocal}, nil
	}
	strategy := Strategy(stored.AgentStrategy.String)
	if stored.TargetMode != string(TargetLabel) || !stored.AgentLabelID.Valid ||
		(strategy != StrategyRoundRobin && strategy != StrategyAll) {
		return Policy{}, fmt.Errorf(
			"%w: stored project policy",
			ErrInvalidPolicy,
		)
	}
	return r.labelPolicy(
		ctx,
		SourceProject,
		stored.AgentLabelID.Int64,
		strategy,
	)
}

func (r *Resolver) labelPolicy(
	ctx context.Context,
	source Source,
	labelID int64,
	strategy Strategy,
) (Policy, error) {
	label, err := r.repo.Queries.GetAgentLabel(ctx, labelID)
	if errors.Is(err, sql.ErrNoRows) {
		return Policy{}, fmt.Errorf("%w: %d", ErrLabelNotFound, labelID)
	}
	if err != nil {
		return Policy{}, fmt.Errorf("get agent label: %w", err)
	}
	return Policy{
		Source: source, Mode: TargetLabel, LabelID: label.ID,
		LabelName: label.Name, Strategy: strategy,
	}, nil
}
