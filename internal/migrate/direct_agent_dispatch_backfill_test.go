package migrate

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestMigration_BackfillsSingletonPool(t *testing.T) {
	// Given: a pooled version-27 schema with queued and historical dispatches.
	conn := newRemoteAgentSQLiteDB(t)
	migrateRemoteAgentSQLiteTo(t, conn, 27)
	seedDirectDispatchBackfillFixture(t, conn, false)
	seedLatestEnvironmentAssignment(t, conn)
	_, err := conn.Exec(`
		INSERT INTO agent_pools (id, name) VALUES (100, 'deleted-high-water');
		DELETE FROM agent_pools WHERE id = 100`)
	if err != nil {
		t.Fatalf("seed deleted SQLite high-water pool ID: %v", err)
	}

	// When: the direct-assignment migration replaces pool routing.
	if err := goose.UpTo(conn, ".", 28); err != nil {
		t.Fatalf("apply direct-assignment migration: %v", err)
	}

	// Then: every remote dispatch retains or receives its deterministic agent.
	want := map[int64]string{
		1: "environment-agent", 2: "in-flight-agent", 3: "history-agent", 4: "assigned-agent",
	}
	for deploymentID, agentID := range want {
		var got string
		err := conn.QueryRow(`
			SELECT assigned_agent_id FROM deployment_dispatches
			WHERE deployment_id = ?`, deploymentID).Scan(&got)
		if err != nil {
			t.Fatalf("read deployment %d assignment: %v", deploymentID, err)
		}
		if got != agentID {
			t.Fatalf(
				"deployment %d assigned agent = %q, want %q",
				deploymentID,
				got,
				agentID,
			)
		}
	}

	if err := goose.Down(conn, "."); err != nil {
		t.Fatalf("roll back direct-assignment migration: %v", err)
	}
	var count int
	err = conn.QueryRow(
		"SELECT COUNT(*) FROM deployment_dispatches WHERE deployment_id = 1",
	).Scan(&count)
	if err != nil {
		t.Fatalf("read queued legacy dispatch after rollback: %v", err)
	}
	if count != 1 {
		t.Fatalf("queued legacy dispatches after rollback = %d, want 1", count)
	}
	var nextPoolID int64
	if err := conn.QueryRow(
		"INSERT INTO agent_pools (name) VALUES ('next-pool') RETURNING id",
	).Scan(&nextPoolID); err != nil {
		t.Fatalf("insert SQLite pool after rollback: %v", err)
	}
	if nextPoolID <= 100 {
		t.Fatalf("SQLite next pool ID = %d, want above 100", nextPoolID)
	}
}

func TestMigration_AbortsOnAmbiguousPool(t *testing.T) {
	for _, memberCount := range []int{0, 2} {
		t.Run("members="+string(rune('0'+memberCount)), func(t *testing.T) {
			// Given: a queued deployment whose pool does not resolve to one active agent.
			conn := newRemoteAgentSQLiteDB(t)
			migrateRemoteAgentSQLiteTo(t, conn, 27)
			seedDirectDispatchBackfillFixture(t, conn, memberCount == 2)
			if memberCount == 0 {
				_, err := conn.Exec(
					"DELETE FROM agent_pool_memberships WHERE pool_id = 1",
				)
				if err != nil {
					t.Fatalf("remove singleton pool member: %v", err)
				}
			}

			// When: the migration tries to replace the queued pool route.
			err := goose.UpTo(conn, ".", 28)

			// Then: it refuses before changing the legacy table.
			if err == nil {
				t.Fatal("ambiguous pool migration succeeded")
			}
			if !strings.Contains(
				err.Error(),
				"cannot migrate pooled deployment dispatches for environments: 1",
			) {
				t.Fatalf("migration error = %q", err)
			}
			var count int
			err = conn.QueryRow(
				"SELECT COUNT(*) FROM deployment_dispatches WHERE deployment_id = 1",
			).Scan(&count)
			if err != nil {
				t.Fatalf("read queued legacy dispatch after refusal: %v", err)
			}
			if count != 1 {
				t.Fatalf(
					"queued legacy dispatches after refusal = %d, want 1",
					count,
				)
			}
		})
	}
}

func TestMSSQL_DirectAgentDispatchBackfillParity(t *testing.T) {
	t.Run("singleton", func(t *testing.T) {
		conn := newSQLServerTestDBAt(t, 12)
		seedDirectDispatchBackfillFixture(t, conn, false)
		seedLatestEnvironmentAssignment(t, conn)
		_, err := conn.Exec(`
			SET IDENTITY_INSERT agent_pools ON;
			INSERT INTO agent_pools (id, name) VALUES (100, 'deleted-high-water');
			SET IDENTITY_INSERT agent_pools OFF;
			DELETE FROM agent_pools WHERE id = 100`)
		if err != nil {
			t.Fatalf("seed deleted SQL Server high-water pool ID: %v", err)
		}
		_, err = conn.Exec(`
			UPDATE agent_pools
			SET name = 'original-pool', description = 'original description',
			    enabled = 0, created_at = 11, updated_at = 12
			WHERE id = 1;
			UPDATE environment_agent_policies
			SET selector = 'region=west', created_at = 13, updated_at = 14
			WHERE environment_id = 1;
			INSERT INTO agent_tags (agent_id, tag_key, tag_value, created_at)
			VALUES ('pool-agent', 'region', 'west', 15)`)
		if err != nil {
			t.Fatalf("seed rollback metadata: %v", err)
		}
		migrateDirectDispatchMSSQLTo(t, conn, 13)

		for deploymentID, want := range map[int64]string{
			1: "environment-agent", 2: "in-flight-agent", 3: "history-agent", 4: "assigned-agent",
		} {
			var got string
			err := conn.QueryRow(`
				SELECT assigned_agent_id FROM deployment_dispatches
				WHERE deployment_id = ?`, deploymentID).Scan(&got)
			if err != nil {
				t.Fatalf("read deployment %d assignment: %v", deploymentID, err)
			}
			if got != want {
				t.Fatalf(
					"deployment %d assigned agent = %q, want %q",
					deploymentID,
					got,
					want,
				)
			}
		}
		if err := goose.Down(conn, "."); err != nil {
			t.Fatalf("roll back direct-assignment migration: %v", err)
		}
		var poolName, description string
		var enabled, createdAt, updatedAt int64
		err = conn.QueryRow(`
			SELECT name, description, enabled, created_at, updated_at
			FROM agent_pools WHERE id = 1`).Scan(
			&poolName, &description, &enabled, &createdAt, &updatedAt,
		)
		if err != nil {
			t.Fatalf("read pool after rollback: %v", err)
		}
		if poolName != "original-pool" ||
			description != "original description" ||
			enabled != 0 ||
			createdAt != 11 ||
			updatedAt != 12 {
			t.Fatalf(
				"pool after rollback = (%q, %q, %d, %d, %d), want original values",
				poolName,
				description,
				enabled,
				createdAt,
				updatedAt,
			)
		}
		var nextPoolID int64
		err = conn.QueryRow(
			"INSERT INTO agent_pools (name) OUTPUT INSERTED.id VALUES ('next-pool')",
		).Scan(&nextPoolID)
		if err != nil {
			t.Fatalf("insert SQL Server pool after rollback: %v", err)
		}
		if nextPoolID <= 100 {
			t.Fatalf("SQL Server next pool ID = %d, want above 100", nextPoolID)
		}
	})

	t.Run("ambiguous", func(t *testing.T) {
		conn := newSQLServerTestDBAt(t, 12)
		seedDirectDispatchBackfillFixture(t, conn, true)
		config, err := migrationConfig("sqlserver://host/database")
		if err != nil {
			t.Fatalf("build SQL Server migration config: %v", err)
		}
		goose.SetBaseFS(config.migrationFS)
		if err := goose.SetDialect(config.gooseDialect); err != nil {
			t.Fatalf("set SQL Server Goose dialect: %v", err)
		}
		err = goose.UpTo(conn, ".", 13)
		if err == nil {
			t.Fatal("ambiguous pool migration succeeded")
		}
		if !strings.Contains(
			err.Error(),
			"cannot migrate pooled deployment dispatches for environments: 1",
		) {
			t.Fatalf("migration error = %q", err)
		}
	})

	t.Run(
		"environment-assignment-precedes-multiple-pool-members",
		func(t *testing.T) {
			// Given: a waiting row has a current environment assignment and an
			// otherwise ambiguous legacy pool.
			conn := newSQLServerTestDBAt(t, 12)
			seedDirectDispatchBackfillFixture(t, conn, true)
			seedLatestEnvironmentAssignment(t, conn)

			// When: direct assignment migration runs.
			migrateDirectDispatchMSSQLTo(t, conn, 13)

			// Then: the environment assignment is the single deterministic result.
			var assignedAgentID string
			if err := conn.QueryRow(`
			SELECT assigned_agent_id FROM deployment_dispatches
			WHERE deployment_id = 1`).Scan(&assignedAgentID); err != nil {
				t.Fatalf("read migrated environment assignment: %v", err)
			}
			if assignedAgentID != "environment-agent" {
				t.Fatalf(
					"migrated environment assignment = %q, want environment-agent",
					assignedAgentID,
				)
			}
		},
	)

	t.Run("active-and-inactive", func(t *testing.T) {
		conn := newSQLServerTestDBAt(t, 12)
		seedDirectDispatchBackfillFixture(t, conn, false)
		_, err := conn.Exec(`
			INSERT INTO agents (
				id, name, status, certificate_pem, certificate_fingerprint
			) VALUES ('inactive-pool-agent', 'inactive-pool-agent', 'disabled', 'certificate', ?)`, strings.Repeat("z", 64))
		if err != nil {
			t.Fatalf("seed inactive agent: %v", err)
		}
		_, err = conn.Exec(`
			INSERT INTO agent_pool_memberships (pool_id, agent_id, created_at)
			VALUES (1, 'inactive-pool-agent', 20)`)
		if err != nil {
			t.Fatalf("seed inactive pool member: %v", err)
		}
		config, err := migrationConfig("sqlserver://host/database")
		if err != nil {
			t.Fatalf("build SQL Server migration config: %v", err)
		}
		goose.SetBaseFS(config.migrationFS)
		if err := goose.SetDialect(config.gooseDialect); err != nil {
			t.Fatalf("set SQL Server Goose dialect: %v", err)
		}
		err = goose.UpTo(conn, ".", 13)
		if err == nil || !strings.Contains(
			err.Error(),
			"cannot migrate pooled deployment dispatches for environments: 1",
		) {
			t.Fatalf("active and inactive pool migration error = %v", err)
		}
	})
}

func TestPostgres_DirectAgentDispatchBackfillParity(t *testing.T) {
	conn := newPostgresTestDBAt(t, 27)
	seedDirectDispatchBackfillFixture(t, conn, false)
	seedLatestEnvironmentAssignment(t, conn)
	_, err := conn.Exec(`
		INSERT INTO agent_pools (id, name) VALUES (100, 'deleted-high-water');
		DELETE FROM agent_pools WHERE id = 100;
		SELECT setval('agent_pools_id_seq', 100, true)`)
	if err != nil {
		t.Fatalf("seed deleted PostgreSQL high-water pool ID: %v", err)
	}
	if err := goose.UpTo(conn, ".", 28); err != nil {
		t.Fatalf("apply direct-assignment migration: %v", err)
	}
	var got string
	if err := conn.QueryRow(`SELECT assigned_agent_id FROM deployment_dispatches WHERE deployment_id = 1`).
		Scan(&got); err != nil {
		t.Fatalf("read queued assignment: %v", err)
	}
	if got != "environment-agent" {
		t.Fatalf("queued assigned agent = %q, want environment-agent", got)
	}
	if err := goose.Down(conn, "."); err != nil {
		t.Fatalf("roll back direct-assignment migration: %v", err)
	}
	var nextPoolID int64
	if err := conn.QueryRow(
		"INSERT INTO agent_pools (name) VALUES ('next-pool') RETURNING id",
	).Scan(&nextPoolID); err != nil {
		t.Fatalf("insert PostgreSQL pool after rollback: %v", err)
	}
	if nextPoolID <= 100 {
		t.Fatalf("PostgreSQL next pool ID = %d, want above 100", nextPoolID)
	}
}

func migrateDirectDispatchMSSQLTo(t *testing.T, conn *sql.DB, version int64) {
	t.Helper()
	config, err := migrationConfig("sqlserver://host/database")
	if err != nil {
		t.Fatalf("build SQL Server migration config: %v", err)
	}
	goose.SetBaseFS(config.migrationFS)
	if err := goose.SetDialect(config.gooseDialect); err != nil {
		t.Fatalf("set SQL Server Goose dialect: %v", err)
	}
	if err := goose.UpTo(conn, ".", version); err != nil {
		t.Fatalf("apply direct-assignment migration: %v", err)
	}
}

func seedLatestEnvironmentAssignment(t *testing.T, conn *sql.DB) {
	t.Helper()
	_, err := conn.Exec(`
		INSERT INTO agents (id, name, status, certificate_pem, certificate_fingerprint)
		VALUES ('environment-agent', 'environment-agent', 'active', 'certificate', ?)`, strings.Repeat("e", 64))
	if err != nil {
		t.Fatalf("create environment agent: %v", err)
	}
	_, err = conn.Exec(`
		INSERT INTO environment_agent_assignments (environment_id, agent_id, updated_at)
		VALUES (1, 'environment-agent', 20)`)
	if err != nil {
		t.Fatalf("create latest environment assignment: %v", err)
	}
}

func seedDirectDispatchBackfillFixture(
	t *testing.T,
	conn *sql.DB,
	includeSecondPoolMember bool,
) {
	t.Helper()
	for _, agentID := range []string{
		"pool-agent", "in-flight-agent", "history-agent", "assigned-agent",
	} {
		_, err := conn.Exec(`
			INSERT INTO agents (
				id, name, status, certificate_pem, certificate_fingerprint
			) VALUES (?, ?, 'active', 'certificate', ?)`,
			agentID,
			agentID,
			strings.Repeat(agentID[:1], 64),
		)
		if err != nil {
			t.Fatalf("create %s: %v", agentID, err)
		}
	}
	if includeSecondPoolMember {
		_, err := conn.Exec(`
			INSERT INTO agents (
				id, name, status, certificate_pem, certificate_fingerprint
			) VALUES ('second-pool-agent', 'second-pool-agent', 'active', 'certificate', ?)`,
			strings.Repeat("s", 64),
		)
		if err != nil {
			t.Fatalf("create second pool member: %v", err)
		}
	}
	_, err := conn.Exec(`
		INSERT INTO projects (name) VALUES ('backfill-project');
		INSERT INTO environments (name) VALUES ('backfill-environment');
		INSERT INTO releases (project_id, version, steps_json)
		VALUES (1, 'backfill-release', '[]');
		INSERT INTO agent_pools (name) VALUES ('backfill-pool');
		INSERT INTO agent_pool_memberships (pool_id, agent_id, created_at)
		VALUES (1, 'pool-agent', 10);
		INSERT INTO environment_agent_policies (environment_id, pool_id)
		VALUES (1, 1);
		INSERT INTO deployments (release_id, environment_id, status)
		VALUES (1, 1, 'pending'), (1, 1, 'running'),
		       (1, 1, 'succeeded'), (1, 1, 'pending');
		INSERT INTO deployment_dispatches (
			deployment_id, mode, pool_id, state
		) VALUES (1, 'remote', 1, 'waiting');
		INSERT INTO deployment_dispatches (
			deployment_id, mode, pool_id, state, agent_id
		) VALUES (2, 'remote', 1, 'claimed', 'in-flight-agent');
		INSERT INTO deployment_dispatches (
			deployment_id, mode, pool_id, state, agent_id
		) VALUES (3, 'remote', 1, 'succeeded', 'history-agent');
		INSERT INTO deployment_dispatches (
			deployment_id, mode, assigned_agent_id, state
		) VALUES (4, 'remote', 'assigned-agent', 'waiting');`)
	if err != nil {
		t.Fatalf("seed direct dispatch backfill fixture: %v", err)
	}
	if includeSecondPoolMember {
		_, err = conn.Exec(`
			INSERT INTO agent_pool_memberships (pool_id, agent_id, created_at)
			VALUES (1, 'second-pool-agent', 20)`)
		if err != nil {
			t.Fatalf("add second pool member: %v", err)
		}
	}
}
