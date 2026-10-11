package runner

import (
	"os"
	"testing"
	"time"
)

func TestCancellationDuringStagingExportPreservesCancelledStatus(t *testing.T) {
	r, repo, trace := podmanFixture(t, `
case "$3" in
exec) printf exporting > "$PODMAN_TRACE"; exec sleep 30;;
esac
`)
	dep := stagingDeployment(t, repo)
	if _, err := repo.DB.ExecContext(
		t.Context(),
		`UPDATE deployment_step_sources SET steps_json=? WHERE deployment_id=?`,
		`[{"name":"producer","container_image":"example/worker:1"},{"name":"consumer","execution_target":"agent"}]`,
		dep.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(
		t.Context(),
		`UPDATE deployment_steps SET execution_target='agent' WHERE deployment_id=? AND step_index=1`,
		dep.ID,
	); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(t.Context(), dep.ID, dep.ReleaseID, dep.EnvironmentID)
	}()
	t.Cleanup(func() {
		_ = r.Cancel(dep.ID)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("staging export did not stop")
		}
	})
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(trace); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-done:
			stored, _ := repo.Queries.GetDeployment(t.Context(), dep.ID)
			logs, _ := repo.Queries.ListDeploymentLogsByDeployment(
				t.Context(),
				dep.ID,
			)
			t.Fatalf(
				"deployment stopped before staging export: status=%s logs=%+v",
				stored.Status,
				logs,
			)
		case <-deadline.C:
			t.Fatal("staging export did not start")
		case <-tick.C:
		}
	}
	if err := r.Cancel(dep.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled staging export did not finish")
	}
	stored, err := repo.Queries.GetDeployment(t.Context(), dep.ID)
	if err != nil || stored.Status != "cancelled" || !stored.FinishedAt.Valid {
		t.Fatalf("cancelled staging deployment=%+v error=%v", stored, err)
	}
	if len(r.staging) != 0 || len(r.transfers) != 0 {
		t.Fatal("cancelled transfer retained runtime staging")
	}
}
