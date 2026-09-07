package deploymentstate

import (
	"context"
	"path/filepath"
	"testing"

	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestDeploymentFanoutView_FailedScheduledIntent(t *testing.T) {
	// Given
	connection, err := migrate.Run(filepath.Join(t.TempDir(), "routing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	_, err = connection.Exec(`
INSERT INTO projects(id,name) VALUES(1,'project');
INSERT INTO environments(id,name) VALUES(1,'environment');
INSERT INTO releases(id,project_id,version,steps_json) VALUES(1,1,'v1','[]');
INSERT INTO agent_labels(id,name,normalized_name) VALUES(1,'Frozen','frozen');
INSERT INTO deployments(id,release_id,environment_id,status) VALUES(1,1,1,'failed');
INSERT INTO scheduled_deployments(id,project_id,release_id,environment_id,cron,next_run_at) VALUES(1,1,1,1,'0 * * * *',1);
INSERT INTO scheduled_deployment_occurrences(scheduled_deployment_id,due_at,deployment_id,routing_source,target_mode,agent_label_id,agent_label_name,agent_strategy) VALUES(1,1,1,'schedule','label',1,'Frozen','all');`)
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.New(connection)
	// When
	routing, err := Load(context.Background(), repo, 1)
	// Then
	if err != nil || routing.Mode != "fanout" || routing.Source != "schedule" ||
		routing.AggregateStatus != "failed" ||
		routing.AgentLabelName != "Frozen" {
		t.Fatalf("routing=%#v error=%v", routing, err)
	}
	parent, err := IsParent(context.Background(), repo, 1)
	if err != nil || !parent {
		t.Fatalf("parent=%t error=%v", parent, err)
	}
}
