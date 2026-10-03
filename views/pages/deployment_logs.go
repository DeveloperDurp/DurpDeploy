package pages

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"durpdeploy/internal/db"
	"durpdeploy/internal/httpstream"
)

type deploymentLogPanel struct {
	Index int                  `json:"index"`
	Name  string               `json:"name"`
	State string               `json:"state"`
	Open  bool                 `json:"-"`
	Live  []httpstream.StepLog `json:"live"`
	Logs  []db.DeploymentLog   `json:"-"`
}

type deploymentLogView struct {
	Panels []deploymentLogPanel `json:"panels"`
	LastID int64                `json:"lastID"`
}

func deploymentLogPanels(
	release db.Release,
	deployment db.Deployment,
	logs []db.DeploymentLog,
) deploymentLogView {
	steps := parseReleaseSteps(release.StepsJson)
	view := deploymentLogView{Panels: make([]deploymentLogPanel, len(steps)+1)}
	for index, step := range steps {
		view.Panels[index] = deploymentLogPanel{
			Index: index,
			Name:  step.Name,
			State: "pending",
			Live:  []httpstream.StepLog{},
		}
	}
	view.Panels[len(steps)] = deploymentLogPanel{
		Index: -1,
		Name:  "Deployment messages",
		State: "unknown",
		Open:  true,
		Live:  []httpstream.StepLog{},
	}
	ordered := append([]db.DeploymentLog(nil), logs...)
	sort.Slice(
		ordered,
		func(i, j int) bool { return ordered[i].ID < ordered[j].ID },
	)
	for _, log := range ordered {
		position := len(steps)
		if log.StepIndex.Valid && log.StepIndex.Int64 < int64(len(steps)) &&
			log.StepIndex.Int64 >= 0 {
			position = int(log.StepIndex.Int64)
		} else if log.StepName.Valid {
			// Legacy rows are grouped by name only when the match is unambiguous.
			match := -1
			for index, step := range steps {
				if log.StepName.String == step.Name || strings.HasPrefix(log.StepName.String, step.Name+" @ ") {
					if match != -1 {
						match = -1
						break
					}
					match = index
				}
			}
			if match != -1 {
				position = match
			}
		}
		panel := &view.Panels[position]
		panel.Logs = append(panel.Logs, log)
		if log.StepState.Valid {
			panel.State = log.StepState.String
		} else if panel.State == "pending" {
			panel.State = "unknown"
		}
		if log.ID > view.LastID {
			view.LastID = log.ID
		}
	}
	for index := range steps {
		panel := &view.Panels[index]
		terminal := isTerminalStatus(deployment.Status) ||
			deployment.Status == "cleanup_unconfirmed"
		if terminal &&
			(panel.State == "running" || panel.State == "waiting") {
			panel.State = "unknown"
		}
		if terminal && panel.State == "pending" {
			panel.State = "unknown"
		}
		panel.Open = panel.State == "running" || panel.State == "waiting" ||
			panel.State == "failed" ||
			panel.State == "unknown"
	}
	return view
}

func deploymentActiveStepLabel(view deploymentLogView, status string) string {
	for _, panel := range view.Panels {
		if panel.Index >= 0 &&
			(panel.State == "running" || panel.State == "waiting") {
			return fmt.Sprintf(
				"%s: Step %d — %s",
				deploymentStepStateLabel(panel.State),
				panel.Index+1,
				panel.Name,
			)
		}
	}
	if isTerminalStatus(status) || status == "cleanup_unconfirmed" {
		return "Deployment " + strings.ReplaceAll(status, "_", " ")
	}
	if status == "running" {
		return "Current step unavailable"
	}
	return "Waiting to start"
}

func deploymentLogConfig(view deploymentLogView) string {
	data, err := json.Marshal(view)
	if err != nil {
		panic(err)
	} // Only concrete primitive fields are serialized.
	return string(data)
}

func deploymentStepStateLabel(state string) string {
	switch state {
	case "waiting":
		return "Waiting for agents"
	case "running":
		return "Running"
	case "succeeded":
		return "Succeeded"
	case "failed":
		return "Failed"
	case "cancelled":
		return "Cancelled"
	case "not_run":
		return "Not run"
	case "unknown":
		return "State unavailable"
	default:
		return "Pending"
	}
}
