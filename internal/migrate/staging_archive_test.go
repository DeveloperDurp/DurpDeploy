package migrate

import (
	"database/sql"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/staging"
	"durpdeploy/internal/testutil"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestStagingArchiveUpgradeAndGuardedRollback(t *testing.T) {
	forEachRemoteClaimDatabase(t, checkStagingArchiveUpgrade)
}

func checkStagingArchiveUpgrade(t *testing.T, fixture remoteClaimTestDB) {
	fixture.baselineVersion = 69
	if fixture.gooseDialect == "mssql" {
		fixture.baselineVersion = 54
	}
	conn := fixture.openBaseline(t)
	t.Cleanup(func() { testutil.Close(t, conn) })
	seedArchiveUpgrade(t, conn)
	q := db.New(conn)
	key := db.GetStagingSnapshotParams{DeploymentID: 1}
	before, err := q.GetStagingSnapshot(t.Context(), key)
	require.NoError(t, err)
	version := fixture.baselineVersion + 1
	require.NoError(t, goose.UpTo(conn, ".", version))
	require.NoError(t, goose.UpTo(conn, ".", version))
	assertArchiveUpgradePreserved(t, conn, before)
	setArchiveUpgradeSize(t, conn, staging.MaxArchive+1, true)
	require.Error(t, goose.DownTo(conn, ".", fixture.baselineVersion))
	gotVersion, err := goose.GetDBVersion(conn)
	require.NoError(t, err)
	require.Equal(t, version, gotVersion)
	retained := before
	retained.Size = staging.MaxArchive + 1
	assertArchiveUpgradePreserved(t, conn, retained)
	for _, size := range []int64{0, -1} {
		setArchiveUpgradeSize(t, conn, size, false)
	}
	setArchiveUpgradeSize(t, conn, before.Size, true)
	require.NoError(t, goose.DownTo(conn, ".", fixture.baselineVersion))
	assertArchiveUpgradePreserved(t, conn, before)
	setArchiveUpgradeSize(t, conn, staging.MaxArchive+1, false)
	require.NoError(t, goose.UpTo(conn, ".", version))
	assertArchiveUpgradePreserved(t, conn, before)
	_, err = conn.ExecContext(t.Context(), "DELETE FROM deployments WHERE id=1")
	require.NoError(t, err)
	for _, table := range []string{"deployment_staging_snapshots", "deployment_staging_chunks"} {
		var count int
		require.NoError(
			t,
			conn.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).
				Scan(&count),
		)
		require.Zero(t, count, "deployment deletion did not cascade")
	}
	t.Logf(
		"%s: preserved hashes/chunks, raised ceiling, rejected unsafe rollback, retained cascades",
		fixture.name,
	)
}

func seedArchiveUpgrade(t *testing.T, conn *sql.DB) {
	t.Helper()
	for _, query := range []string{
		"INSERT INTO projects(name) VALUES('archive-upgrade')",
		"INSERT INTO environments(name) VALUES('archive-upgrade')",
		"INSERT INTO releases(project_id,version,steps_json) VALUES(1,'archive-upgrade','[]')",
		"INSERT INTO deployments(release_id,environment_id,status) VALUES(1,1,'running')",
	} {
		_, err := conn.ExecContext(t.Context(), query)
		require.NoError(t, err)
	}
	q := db.New(conn)
	require.NoError(
		t,
		q.CreateStagingSnapshot(t.Context(), db.CreateStagingSnapshotParams{
			DeploymentID: 1, Sha256: strings.Repeat("a", 64), Size: 2048,
		}),
	)
	require.NoError(
		t,
		q.CreateStagingChunk(t.Context(), db.CreateStagingChunkParams{
			DeploymentID: 1, Ciphertext: "retained-encrypted-chunk",
		}),
	)
	setArchiveUpgradeSize(t, conn, staging.MaxArchive+1, false)
}

func setArchiveUpgradeSize(
	t *testing.T,
	conn *sql.DB,
	size int64,
	accepted bool,
) {
	t.Helper()
	_, err := conn.ExecContext(
		t.Context(),
		"UPDATE deployment_staging_snapshots SET size=? WHERE deployment_id=1",
		size,
	)
	if accepted {
		require.NoError(t, err)
	} else {
		require.Error(t, err)
	}
}

func assertArchiveUpgradePreserved(
	t *testing.T,
	conn *sql.DB,
	want db.DeploymentStagingSnapshot,
) {
	t.Helper()
	q := db.New(conn)
	got, err := q.GetStagingSnapshot(
		t.Context(),
		db.GetStagingSnapshotParams{DeploymentID: 1},
	)
	require.NoError(t, err)
	require.Equal(t, want, got)
	chunk, err := q.GetStagingChunk(
		t.Context(),
		db.GetStagingChunkParams{DeploymentID: 1},
	)
	require.NoError(t, err)
	require.Equal(t, "retained-encrypted-chunk", chunk)
	var count int
	require.NoError(
		t,
		conn.QueryRowContext(t.Context(), "SELECT count(*) FROM eligible_remote_step_agents").
			Scan(&count),
	)
	_, err = conn.ExecContext(
		t.Context(),
		"INSERT INTO deployment_staging_snapshots(deployment_id,step_index,producer,sha256,size) VALUES(999,0,'','invalid',1024)",
	)
	require.Error(t, err, "migration removed deployment foreign key")
}
