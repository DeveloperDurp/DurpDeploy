package pages

import (
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/deploymentstate"
)

func TestCanCancelDeployment_supportedStates(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		routing    deploymentstate.Dispatch
		wantCancel bool
	}{
		{
			name: "pending remote waiting", status: "pending",
			routing: deploymentstate.Dispatch{
				Mode:  "remote",
				State: "waiting",
			},
			wantCancel: true,
		},
		{
			name: "pending remote claimed", status: "pending",
			routing: deploymentstate.Dispatch{
				Mode:  "remote",
				State: "claimed",
			},
			wantCancel: true,
		},
		{
			name: "running remote started", status: "running",
			routing: deploymentstate.Dispatch{
				Mode:  "remote",
				State: "started",
			},
			wantCancel: true,
		},
		{
			name: "running local", status: "running",
			routing: deploymentstate.Dispatch{Mode: "local"}, wantCancel: true,
		},
		{
			name: "pending approval", status: "pending_approval",
			routing: deploymentstate.Dispatch{Mode: "local"}, wantCancel: true,
		},
		{
			name: "pending local", status: "pending",
			routing: deploymentstate.Dispatch{Mode: "local"}, wantCancel: false,
		},
		{
			name: "running remote claimed", status: "running",
			routing: deploymentstate.Dispatch{
				Mode:  "remote",
				State: "claimed",
			},
			wantCancel: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			deployment := db.Deployment{Status: test.status}

			// When
			got := canCancelDeployment(deployment, test.routing)

			// Then
			if got != test.wantCancel {
				t.Fatalf(
					"canCancelDeployment() = %t, want %t",
					got,
					test.wantCancel,
				)
			}
		})
	}
}
