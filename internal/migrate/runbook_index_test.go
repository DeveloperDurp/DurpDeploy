package migrate

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"

	"durpdeploy/migrations"
)

func TestRunbookScheduleIndexUpgrade(t *testing.T) {
	for _, alreadyIndexed := range []bool{false, true} {
		name := "without prior index"
		if alreadyIndexed {
			name = "with prior index"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runbooks.db")
			conn, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			goose.SetBaseFS(migrations.FS)
			if err := goose.SetDialect("sqlite3"); err != nil {
				t.Fatal(err)
			}
			if err := goose.UpTo(conn, ".", 37); err != nil {
				t.Fatal(err)
			}
			if alreadyIndexed {
				_, err = conn.Exec(`CREATE INDEX idx_runbook_executions_schedule
ON runbook_executions(schedule_id)`)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := goose.Up(conn, "."); err != nil {
				t.Fatal(err)
			}
			var count int
			err = conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master
WHERE type = 'index' AND name = 'idx_runbook_executions_schedule'`).
				Scan(&count)
			if err != nil || count != 1 {
				t.Fatalf("schedule index count=%d err=%v", count, err)
			}
		})
	}
}
