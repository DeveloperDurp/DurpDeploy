package mssqldriver

import "testing"

func TestRewriteSQL_RemoteAgentSourceShapes(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name: "rewrites direct environment assignment upsert",
			query: "INSERT INTO environment_agent_assignments (\n" +
				"    environment_id, agent_id, updated_at\n" +
				") VALUES (?, ?, ?)\n" +
				"ON CONFLICT(environment_id) DO UPDATE\n" +
				"SET agent_id = excluded.agent_id,\n" +
				"    updated_at = excluded.updated_at\n" +
				"RETURNING environment_id, agent_id, created_at, updated_at\n",
			want: "MERGE environment_agent_assignments WITH (HOLDLOCK) AS target\n" +
				"\tUSING (SELECT @p1 AS environment_id, @p2 AS agent_id, @p3 AS updated_at) AS source\n" +
				"\tON target.environment_id = source.environment_id\n" +
				"\tWHEN NOT MATCHED THEN INSERT (environment_id, agent_id, updated_at)\n" +
				"\tVALUES (source.environment_id, source.agent_id, source.updated_at)\n" +
				"\tWHEN MATCHED THEN UPDATE SET agent_id = source.agent_id, updated_at = source.updated_at\n" +
				"\tOUTPUT INSERTED.environment_id, INSERTED.agent_id, INSERTED.created_at, INSERTED.updated_at;",
		},
		{
			name: "moves agent enrollment returning to output",
			query: "INSERT INTO agents (id, name, agent_version)\n" +
				"VALUES (?, ?, ?)\n" +
				"RETURNING id, name, status, agent_version, created_at;",
			want: "INSERT INTO agents (id, name, agent_version) OUTPUT " +
				"INSERTED.id, INSERTED.name, INSERTED.status, " +
				"INSERTED.agent_version, INSERTED.created_at\n" +
				"VALUES (@p1, @p2, @p3);",
		},
		{
			name: "moves agent activation returning to output",
			query: "UPDATE agents\n" +
				"SET status = 'active', updated_at = unixepoch()\n" +
				"WHERE id = ? AND status = 'pending'\n" +
				"RETURNING id, status, updated_at;",
			want: "UPDATE agents\n" +
				"SET status = 'active', updated_at = " +
				"DATEDIFF_BIG(SECOND, '1970-01-01T00:00:00Z', " +
				"SYSUTCDATETIME()) OUTPUT INSERTED.id, INSERTED.status, " +
				"INSERTED.updated_at\n" +
				"WHERE id = @p1 AND status = 'pending';",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := RewriteSQL(test.query)
			if err != nil {
				t.Fatalf("RewriteSQL() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("RewriteSQL() = %q, want %q", got, test.want)
			}
		})
	}
}
