package mssqldriver

import "testing"

func TestRewriteSQL_FinalWaveSourceShapes(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "aliases homepage latest deployment derived table",
			query: "SELECT id\nFROM (\n    SELECT id, ROW_NUMBER() OVER (PARTITION BY d.release_id, d.environment_id ORDER BY d.created_at DESC) AS rn\n    FROM deployments d\n) WHERE rn = 1\n",
			want:  "SELECT id\nFROM (\n    SELECT id, ROW_NUMBER() OVER (PARTITION BY d.release_id, d.environment_id ORDER BY d.created_at DESC) AS rn\n    FROM deployments d\n) AS latest_deployment WHERE rn = 1\n",
		},
		{
			name:  "widens source backed filter integer casts outside literals",
			query: "SELECT * FROM deployments d WHERE CAST(?1 AS INTEGER) = 2147483648 AND note = 'CAST(?1 AS INTEGER)'\n",
			want:  "SELECT * FROM deployments d WHERE CAST(@p1 AS BIGINT) = 2147483648 AND note = 'CAST(?1 AS INTEGER)'\n",
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
