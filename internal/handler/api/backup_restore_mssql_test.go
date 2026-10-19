//go:build e2e && backuprestore && linux

package api_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"durpdeploy/internal/mssqldriver"
	"durpdeploy/internal/testutil"
	mssql "github.com/microsoft/go-mssqldb"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

const restoreMSSQLImage = "harbor.durp.info/microsoft/mssql/server:2022-latest@" +
	"sha256:4402d880dd4c34bfa7d8705e56a86cd6c88da80a1f6bbbe741f999e76264a090"

func restoreMSSQL(t *testing.T, source bool) *restoreNativeDB {
	t.Helper()
	c, dsn := testutil.SQLServerContainer(t, restoreMSSQLImage)
	restorePrivatePort(t, c)
	db := &restoreNativeDB{
		container: c,
		adminDSN:  dsn,
		password:  "App!" + rand.Text(),
	}
	admin := restoreOpen(t, mssqldriver.DriverName, dsn)
	restoreSQL(t, admin, fmt.Sprintf(
		"CREATE LOGIN restore_app WITH PASSWORD = '%s', CHECK_POLICY = OFF",
		db.password,
	))
	if source {
		restoreSQL(t, admin, "CREATE DATABASE restore_app")
		restoreSQL(
			t,
			admin,
			"USE restore_app; CREATE USER restore_app FOR LOGIN restore_app; "+
				"ALTER ROLE db_owner ADD MEMBER restore_app",
		)
	}
	db.appDSN = restoreAppDSN(t, dsn, db.password)
	if err := admin.Close(); err != nil {
		t.Fatal(err)
	}
	return db
}

type restoreMSSQLBackup struct {
	fixture   *restoreFixture
	archive   string
	watermark int64
}

func (b *restoreMSSQLBackup) awaitFresh(t *testing.T, id int64) {
	t.Helper()
	b.watermark = id
	db := b.fixture.native
	admin := restoreOpen(t, mssqldriver.DriverName, db.adminDSN)
	restoreSQL(
		t,
		admin,
		"BACKUP DATABASE restore_app TO DISK = N'/tmp/recovery.bak' "+
			"WITH COPY_ONLY, CHECKSUM, STOP_ON_ERROR",
	)
	if err := admin.Close(); err != nil {
		t.Fatal(err)
	}
	b.archive = filepath.Join(b.fixture.root, "backup", "mssql.bak")
	restoreArchive(t, db.container, [2]string{"/tmp/recovery.bak", b.archive})
}

func (b *restoreMSSQLBackup) recover(t *testing.T) {
	t.Helper()
	restoreTerminate(t, b.fixture.native.container)
	b.fixture.recoverKey(t)
	for _, path := range []string{"", restoreTruncatedArchive(t, b.archive)} {
		failed := restoreMSSQL(t, false)
		if err := restoreMSSQLArchive(t, failed, path); err == nil {
			t.Fatal("SQL Server accepted a missing or truncated archive")
		}
		restoreTerminate(t, failed.container)
	}
	target := restoreMSSQL(t, false)
	if err := restoreMSSQLArchive(t, target, b.archive); err != nil {
		t.Fatalf("native SQL Server restore failed: %v", err)
	}
	admin := restoreOpen(t, mssqldriver.DriverName, target.adminDSN)
	restoreSQL(
		t,
		admin,
		"DBCC CHECKDB (N'restore_app') WITH NO_INFOMSGS, ALL_ERRORMSGS",
	)
	app := restoreOpen(t, mssqldriver.DriverName, target.appDSN)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var orphaned int
	if err := admin.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM restore_app.sys.database_principals "+
			"WHERE name = 'restore_app' AND sid <> SUSER_SID('restore_app')").Scan(&orphaned); err != nil || orphaned != 1 {
		t.Fatalf("fresh login did not create an orphaned user: %v", err)
	}
	var loginError mssql.Error
	if err := app.PingContext(ctx); !errors.As(err, &loginError) ||
		(loginError.Number != 4060 && loginError.Number != 4063 && loginError.Number != 916) {
		t.Fatalf(
			"orphaned database user did not reject application login: %v",
			err,
		)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	restoreSQL(
		t,
		admin,
		"USE restore_app; ALTER USER restore_app WITH LOGIN = restore_app",
	)
	if err := admin.Close(); err != nil {
		t.Fatal(err)
	}
	b.fixture.native = target
	b.fixture.dsn = target.appDSN
	b.fixture.assertNativeRestored(t, b.watermark)
	app = restoreOpen(t, mssqldriver.DriverName, target.appDSN)
	var owner, sysadmin int
	if err := app.QueryRowContext(
		ctx,
		"SELECT IS_MEMBER('db_owner'), IS_SRVROLEMEMBER('sysadmin')",
	).Scan(&owner, &sysadmin); err != nil || owner != 1 || sysadmin != 0 {
		t.Fatalf(
			"restored application permissions: owner=%d sysadmin=%d error=%v",
			owner,
			sysadmin,
			err,
		)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
}

func restoreMSSQLArchive(t *testing.T, db *restoreNativeDB, path string) error {
	t.Helper()
	archive := "/tmp/absent.bak"
	if path != "" {
		archive = "/tmp/recovery.bak"
		restoreUpload(t, db.container, [2]string{path, archive})
		if db.command(t, []string{"chown", "10001:0", archive},
			tcexec.WithUser("0")) != 0 {
			t.Fatal("set owned archive permissions")
		}
	}
	admin := restoreOpen(t, mssqldriver.DriverName, db.adminDSN)
	defer func() {
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	moves, err := restoreMSSQLFiles(ctx, admin, archive)
	if err != nil {
		return err
	}
	_, err = admin.ExecContext(
		ctx,
		"RESTORE DATABASE restore_app FROM DISK = N'"+archive+
			"' WITH CHECKSUM, STOP_ON_ERROR, "+moves,
	)
	return err
}
