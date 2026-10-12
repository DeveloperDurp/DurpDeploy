package db

import (
	"math"
	"strings"
	"testing"

	"durpdeploy/internal/migrate"
)

func TestLogMetadataUsesSparseIndex(t *testing.T) {
	// Given: the migrated schema and the generated production query.
	conn, err := migrate.Run(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// When: SQLite plans the metadata lookup independently of page output.
	rows, err := conn.QueryContext(t.Context(),
		"EXPLAIN QUERY PLAN "+listDeploymentLogStateMetadata,
		int64(1), int64(math.MaxInt64))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log(detail)
		// Then: metadata uses its sparse index, not an output-history range.
		if strings.Contains(detail, "idx_deployment_log_metadata") {
			indexed = true
		} else if strings.Contains(detail, "deployment_logs") ||
			strings.Contains(detail, "SEARCH l ") ||
			strings.Contains(detail, "SCAN l") {
			if !strings.Contains(detail, "INTEGER PRIMARY KEY (rowid=?)") &&
				!strings.Contains(detail, " AND id=?") {
				t.Fatalf("metadata lookup scans output rows: %s", detail)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatal("metadata lookup did not use the sparse index")
	}
}
