package db

import (
	"durpdeploy/internal/testutil"
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
	defer testutil.Close(t, conn)
	// When: SQLite plans the metadata lookup independently of page output.
	rows, err := conn.QueryContext(t.Context(),
		"EXPLAIN QUERY PLAN "+listDeploymentLogStateMetadata,
		"", int64(1), int64(math.MaxInt64))
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, rows)
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

func TestLogHistoryUsesCursorRange(t *testing.T) {
	// Given: a migrated database and the production history query.
	conn, err := migrate.Run(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, conn)
	// When: SQLite plans a deep history cursor.
	rows, err := conn.QueryContext(t.Context(),
		"EXPLAIN QUERY PLAN "+listDeploymentLogsBefore,
		int64(16385), int64(1), int64(10), int64(501))
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, rows)
	bounded := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log(detail)
		bounded = bounded || strings.Contains(detail, "id<?")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// Then: the cursor bounds the index scan, not only the returned rows.
	if !bounded {
		t.Fatal("history query scans rows newer than its cursor")
	}
}
