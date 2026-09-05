package dispatch

import (
	"context"
	"durpdeploy/internal/db"
	"fmt"
	"sync"
	"testing"
)

func TestRoundRobin_ConcurrentRequestsAdvanceWithoutDuplicateCycleTurns(
	t *testing.T,
) {
	fixture := newRoutingFixture(t, 3)
	fixture.repo.DB.SetMaxOpenConns(12)
	const requests = 9
	deployments := make([]db.Deployment, requests)
	for i := range deployments {
		deployments[i] = fixture.createDeployment(t)
	}
	policy := Policy{
		Source: SourceRequest, Mode: TargetLabel, LabelID: fixture.label.ID,
		LabelName: fixture.label.Name, Strategy: StrategyRoundRobin,
	}
	ready := make(chan struct{}, requests)
	start := make(chan struct{})
	results := make(chan string, requests)
	errorsFound := make(chan error, requests)
	var group sync.WaitGroup
	for _, deployment := range deployments {
		group.Add(1)
		go func(deploymentID int64) {
			defer group.Done()
			ready <- struct{}{}
			<-start
			resolver := NewResolver(fixture.repo)
			if err := resolver.Freeze(context.Background(), deploymentID, policy); err != nil {
				errorsFound <- err
				return
			}
			agents, err := resolver.SelectedAgents(
				context.Background(),
				deploymentID,
			)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- agents[0].ID
		}(deployment.ID)
	}
	for range requests {
		<-ready
	}
	close(start)
	group.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent freeze: %v", err)
	}
	close(results)
	counts := map[string]int{}
	for id := range results {
		counts[id]++
	}
	if fmt.Sprint(counts) != "map[agent-a:3 agent-b:3 agent-c:3]" {
		t.Fatalf("round robin counts = %v", counts)
	}
}
