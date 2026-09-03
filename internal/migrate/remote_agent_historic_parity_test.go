package migrate

import (
	"context"
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"durpdeploy/migrations"
)

type historicDeploymentLogs struct {
	deploymentID int64
	ids          []int64
}

func TestPostgres_RemoteAgentHistoricLogsBackfill(t *testing.T) {
	conn := newPostgresTestDBAt(t, 24)
	assertRemoteAgentHistoricLogBackfill(t, conn, 25)
}

func TestMSSQL_RemoteAgentHistoricLogsBackfill(t *testing.T) {
	conn := newSQLServerTestDBAt(t, 9)
	assertRemoteAgentHistoricLogBackfill(t, conn, 10)
}

func newPostgresTestDBAt(t *testing.T, version int64) *sql.DB {
	t.Helper()
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("durpdeploy"),
		postgres.WithUsername("durpdeploy"),
		postgres.WithPassword("postgres"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("PostgreSQL DSN: %v", err)
	}
	conn, err := sql.Open("pgx-qmark", dsn)
	if err != nil {
		t.Fatalf("open PostgreSQL database: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping PostgreSQL database: %v", err)
	}
	goose.SetBaseFS(migrations.PostgresFS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set PostgreSQL Goose dialect: %v", err)
	}
	if err := goose.UpTo(conn, ".", version); err != nil {
		t.Fatalf("migrate PostgreSQL to %d: %v", version, err)
	}
	return conn
}

func assertRemoteAgentHistoricLogBackfill(
	t *testing.T,
	conn *sql.DB,
	remoteAgentVersion int64,
) {
	t.Helper()
	historic := seedHistoricDeploymentLogs(t, conn)

	// Given a pre-agent schema with two persisted historical log rows.
	if err := goose.UpTo(conn, ".", remoteAgentVersion); err != nil {
		t.Fatalf("apply remote-agent migration: %v", err)
	}
	assertSequencedLogs(t, conn, historic)

	// When the legacy writer omits sequence after the migration.
	legacy := appendLegacyDeploymentLogs(t, conn, historic.deploymentID)
	assertNullSequences(t, conn, legacy)
	allLogs := historicDeploymentLogs{
		deploymentID: historic.deploymentID,
		ids:          append(historic.ids, legacy.ids...),
	}
	if err := goose.Down(conn, "."); err != nil {
		t.Fatalf("roll back remote-agent migration: %v", err)
	}
	assertLogRows(t, conn, allLogs)
	if err := goose.UpTo(conn, ".", remoteAgentVersion); err != nil {
		t.Fatalf("reapply remote-agent migration: %v", err)
	}

	// Then the rollback preserves all rows and reapply backfills every sequence.
	assertSequencedLogs(t, conn, allLogs)
}

func seedHistoricDeploymentLogs(
	t *testing.T,
	conn *sql.DB,
) historicDeploymentLogs {
	t.Helper()
	var projectID, environmentID, releaseID, deploymentID int64
	err := conn.QueryRow(
		"INSERT INTO projects (name) VALUES (?) RETURNING id",
		"historic-backfill-project",
	).Scan(&projectID)
	requireNoError(t, err, "create historic project")
	err = conn.QueryRow(
		"INSERT INTO environments (name) VALUES (?) RETURNING id",
		"historic-backfill-environment",
	).Scan(&environmentID)
	requireNoError(t, err, "create historic environment")
	err = conn.QueryRow(`
		INSERT INTO releases (project_id, version, steps_json)
		VALUES (?, ?, ?) RETURNING id`,
		projectID,
		"historic-backfill-release",
		"[]",
	).Scan(&releaseID)
	requireNoError(t, err, "create historic release")
	err = conn.QueryRow(`
		INSERT INTO deployments (release_id, environment_id, status)
		VALUES (?, ?, ?) RETURNING id`,
		releaseID,
		environmentID,
		"pending",
	).Scan(&deploymentID)
	requireNoError(t, err, "create historic deployment")

	ids := make([]int64, 0, 2)
	for _, log := range []struct {
		stepName sql.NullString
		line     string
	}{
		{stepName: sql.NullString{String: "historic-step", Valid: true}, line: "first"},
		{stepName: sql.NullString{}, line: "second"},
	} {
		var id int64
		err = conn.QueryRow(`
			INSERT INTO deployment_logs (deployment_id, step_name, line)
			VALUES (?, ?, ?) RETURNING id`,
			deploymentID,
			log.stepName,
			log.line,
		).Scan(&id)
		requireNoError(t, err, "create historic deployment log")
		ids = append(ids, id)
	}
	return historicDeploymentLogs{deploymentID: deploymentID, ids: ids}
}

func appendLegacyDeploymentLogs(
	t *testing.T,
	conn *sql.DB,
	deploymentID int64,
) historicDeploymentLogs {
	t.Helper()
	ids := make([]int64, 0, 2)
	for _, line := range []string{"legacy-first", "legacy-second"} {
		var id int64
		err := conn.QueryRow(`
			INSERT INTO deployment_logs (deployment_id, step_name, line)
			VALUES (?, ?, ?) RETURNING id`,
			deploymentID,
			"legacy-step",
			line,
		).Scan(&id)
		requireNoError(t, err, "create omitted-sequence deployment log")
		ids = append(ids, id)
	}
	return historicDeploymentLogs{deploymentID: deploymentID, ids: ids}
}

func assertSequencedLogs(
	t *testing.T,
	conn *sql.DB,
	logs historicDeploymentLogs,
) {
	t.Helper()
	rows, err := conn.Query(`
		SELECT id, sequence, line FROM deployment_logs
		WHERE deployment_id = ? ORDER BY id`, logs.deploymentID)
	requireNoError(t, err, "read sequenced historic deployment logs")
	defer rows.Close()
	for _, expectedID := range logs.ids {
		if !rows.Next() {
			t.Fatalf("historic deployment log %d is missing", expectedID)
		}
		var id, sequence int64
		var line string
		if err := rows.Scan(&id, &sequence, &line); err != nil {
			t.Fatalf("scan historic deployment log: %v", err)
		}
		if id != expectedID || sequence != expectedID {
			t.Fatalf(
				"historic deployment log id and sequence = %d, %d; want %d, %d",
				id,
				sequence,
				expectedID,
				expectedID,
			)
		}
	}
	if rows.Next() {
		t.Fatal("unexpected historic deployment log")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate historic deployment logs: %v", err)
	}
}

func assertNullSequences(
	t *testing.T,
	conn *sql.DB,
	logs historicDeploymentLogs,
) {
	t.Helper()
	for _, id := range logs.ids {
		var sequence sql.NullInt64
		err := conn.QueryRow(
			"SELECT sequence FROM deployment_logs WHERE id = ?",
			id,
		).Scan(&sequence)
		requireNoError(t, err, "read omitted-sequence deployment log")
		if sequence.Valid {
			t.Fatalf(
				"omitted deployment log sequence = %d, want NULL",
				sequence.Int64,
			)
		}
	}
}

func assertLogRows(t *testing.T, conn *sql.DB, logs historicDeploymentLogs) {
	t.Helper()
	rows, err := conn.Query(`
		SELECT id, line FROM deployment_logs
		WHERE deployment_id = ? ORDER BY id`, logs.deploymentID)
	requireNoError(t, err, "read deployment logs after rollback")
	defer rows.Close()
	for _, expectedID := range logs.ids {
		if !rows.Next() {
			t.Fatalf("deployment log %d is missing after rollback", expectedID)
		}
		var id int64
		var line string
		if err := rows.Scan(&id, &line); err != nil {
			t.Fatalf("scan deployment log after rollback: %v", err)
		}
		if id != expectedID {
			t.Fatalf(
				"deployment log id after rollback = %d, want %d",
				id,
				expectedID,
			)
		}
	}
	if rows.Next() {
		t.Fatal("unexpected deployment log after rollback")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate deployment logs after rollback: %v", err)
	}
}
