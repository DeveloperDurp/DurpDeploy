package mssqldriver

import "testing"

func TestRewriteSQL_RemoteAgentSourceShapes(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
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
