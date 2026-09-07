package dispatch

import (
	"context"
	"reflect"
	"testing"
)

func TestScheduledDispatchRecovery_PreservesExistingDispatch(t *testing.T) {
	for _, mode := range []string{"local", "label"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newRoutingFixture(t, 3)
			input := Input{Source: SourceRequest, Mode: mode}
			if mode == "label" {
				input.LabelID = fixture.label.ID
				input.Strategy = "round_robin"
			}
			service := NewCreationService(
				fixture.repo, New(fixture.repo, fixture.box, nil),
			)
			root, err := service.Create(context.Background(), CreateRequest{
				ProjectID: fixture.project.ID, ReleaseID: fixture.release.ID,
				EnvironmentID: fixture.environment.ID, Routing: input,
			})
			if err != nil {
				t.Fatal(err)
			}
			before, err := fixture.repo.Queries.GetDeploymentDispatch(
				context.Background(), root.ID,
			)
			if err != nil {
				t.Fatal(err)
			}
			connection := separateRoutingRepositories(t, fixture.repo, 1)[0]
			restarted := NewCreationService(
				connection,
				New(connection, nil, nil),
			)
			if err := restarted.DispatchFrozen(context.Background(), root.ID); err != nil {
				t.Fatalf("replay existing dispatch: %v", err)
			}
			after, err := fixture.repo.Queries.GetDeploymentDispatch(
				context.Background(), root.ID,
			)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("recovery mutated the existing dispatch")
			}
		})
	}
}
