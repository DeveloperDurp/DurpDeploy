package agentserver

import (
	"context"
	"os"
	"testing"
	"time"

	"durpdeploy/internal/migrate"

	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestAgentLabelRoutingRuntimeParity(t *testing.T) {
	engine := os.Getenv("DURPDEPLOY_AGENT_RUNTIME_ENGINE")
	if engine == "" {
		engine = "postgres"
	}
	dsn := labelRuntimeDSN(t, engine)
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate %s: %v", engine, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	scenarios := []struct {
		name string
		run  func(*testing.T, *labelRuntimeFixture)
	}{
		{"RoundRobinConcurrentThreeAgents", runtimeRoundRobin},
		{"FanoutUniqueChildren", runtimeFanout},
		{"ParentConstraints", runtimeParentConstraints},
		{"ScheduleOccurrenceCAS", runtimeScheduleCAS},
		{"ApprovalCAS", runtimeApprovalCAS},
		{"ApprovalRollback", runtimeApprovalRollback},
		{"ExactSetRetryApprovalCAS", runtimeExactApprovalCAS},
		{"ExactSetRetryApprovalRollback", runtimeExactApprovalRollback},
		{"ImmediateAtomicRollback", runtimeImmediateRollback},
		{"ScheduledDispatchRecovery", runtimeScheduledRecovery},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			scenario.run(t, newLabelRuntimeFixture(t, engine, dsn))
		})
	}
}

func labelRuntimeDSN(t *testing.T, engine string) string {
	t.Helper()
	if engine == "mssql" {
		return mssqlRuntimeDB(t)
	}
	if engine != "postgres" {
		t.Fatalf("unsupported runtime engine %q", engine)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("durpdeploy"),
		postgres.WithUsername("durpdeploy"),
		postgres.WithPassword("postgres"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		if os.Getenv("DURPDEPLOY_AGENT_RUNTIME_PARITY_REQUIRED") == "1" {
			t.Fatalf("required PostgreSQL runtime unavailable: %v", err)
		}
		t.Skipf("PostgreSQL runtime unavailable: %v", err)
	}
	t.Logf("owned_container=%s engine=postgres", container.GetContainerID())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := container.Terminate(ctx); err != nil {
			t.Errorf("container cleanup: %v", err)
		} else {
			t.Log("owned_container_cleanup=complete")
		}
	})
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}
