package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"durpdeploy/internal/db"
)

var (
	ErrInvalidAgentLabel = errors.New(
		"label must be between 1 and 64 characters",
	)
	ErrAgentNotFound = errors.New("agent not found")
)

func normalizeAgentLabel(raw string) (string, error) {
	label := strings.TrimSpace(raw)
	if label == "" || len(label) > 64 {
		return "", ErrInvalidAgentLabel
	}
	return label, nil
}

func (r *Repository) AddAgentLabel(
	ctx context.Context,
	agentID, rawLabel string,
) (string, int64, error) {
	label, err := normalizeAgentLabel(rawLabel)
	if err != nil {
		return "", 0, err
	}
	if _, err := r.Queries.GetAgent(ctx, agentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return label, 0, ErrAgentNotFound
		}
		return label, 0, err
	}
	changed, err := r.Queries.AddAgentLabel(ctx, db.AddAgentLabelParams{
		AgentID: agentID, Label: label,
	})
	return label, changed, err
}

func (r *Repository) DeleteAgentLabel(
	ctx context.Context,
	agentID, rawLabel string,
) (string, int64, error) {
	label, err := normalizeAgentLabel(rawLabel)
	if err != nil {
		return "", 0, err
	}
	changed, err := r.Queries.DeleteAgentLabel(ctx, db.DeleteAgentLabelParams{
		AgentID: agentID, Label: label,
	})
	return label, changed, err
}
