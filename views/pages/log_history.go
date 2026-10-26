package pages

import (
	"context"
	"strconv"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type logHistoryKey struct{}
type logHistory struct {
	Page   repository.LogPage
	States []db.ListDeploymentLogStateMetadataRow
}

func WithLogHistory(
	ctx context.Context,
	page repository.LogPage,
	states []db.ListDeploymentLogStateMetadataRow,
) context.Context {
	return context.WithValue(ctx, logHistoryKey{}, logHistory{page, states})
}

func logHistoryPage(ctx context.Context) repository.LogPage {
	history, _ := ctx.Value(logHistoryKey{}).(logHistory)
	return history.Page
}

func logHistoryURL(base string, before int64) string {
	return base + "?before=" + strconv.FormatInt(before, 10)
}

func deploymentPageLogView(
	ctx context.Context,
	release db.Release,
	deployment db.Deployment,
	logs []db.DeploymentLog,
) deploymentLogView {
	history, ok := ctx.Value(logHistoryKey{}).(logHistory)
	if !ok {
		return deploymentLogPanels(release, deployment, logs)
	}
	visible := make(map[int64]bool, len(logs))
	combined := append([]db.DeploymentLog(nil), logs...)
	for _, row := range logs {
		visible[row.ID] = true
	}
	for _, state := range history.States {
		if !visible[state.ID] {
			combined = append(combined, db.DeploymentLog{
				ID: state.ID, StepIndex: state.StepIndex,
				StepName: state.StepName, StepState: state.StepState,
				Line: state.Line,
			})
		}
	}
	view := deploymentLogPanels(release, deployment, combined)
	for i := range view.Panels {
		panel := &view.Panels[i]
		rows := panel.Logs[:0]
		for _, row := range panel.Logs {
			if visible[row.ID] {
				rows = append(rows, row)
			}
		}
		panel.Logs = rows
	}
	view.LastID = history.Page.LastID
	view.Historical = history.Page.Before != 0
	view.OlderBefore = history.Page.NextBefore
	view.Limit = history.Page.Limit
	view.ByteLimit = history.Page.ByteLimit
	return view
}
