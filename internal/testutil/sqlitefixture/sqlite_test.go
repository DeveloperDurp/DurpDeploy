package sqlitefixture_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"durpdeploy/internal/testutil/sqlitefixture"
)

func TestOpenPreservesPragmasAndIsolation(t *testing.T) {
	for _, mode := range []string{"WAL", "DELETE"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "test.db")
			conn := sqlitefixture.Open(t, path,
				"_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&"+
					"_pragma=journal_mode("+mode+")",
			)
			var actualMode string
			var foreignKeys, timeout, count int
			require.NoError(t, conn.QueryRow("PRAGMA journal_mode").
				Scan(&actualMode))
			require.Equal(t, strings.ToLower(mode), actualMode)
			require.NoError(t, conn.QueryRow("PRAGMA foreign_keys").
				Scan(&foreignKeys))
			require.Equal(t, 1, foreignKeys)
			require.NoError(t, conn.QueryRow("PRAGMA busy_timeout").
				Scan(&timeout))
			require.Equal(t, 5000, timeout)
			require.NoError(t, conn.QueryRow("SELECT count(*) FROM projects").
				Scan(&count))
			require.Zero(t, count)
			_, err := conn.Exec("INSERT INTO projects(name) VALUES('isolated')")
			require.NoError(t, err)
		})
	}
}
