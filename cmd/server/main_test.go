package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
)

// tempDSN returns a SQLite file DSN pointing inside t.TempDir() with the same
// pragmas the server uses. Each test gets an isolated database.
func tempDSN(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "durpdeploy-test.db") +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
}

func TestLoadDSN_defaultsToLocalDev(t *testing.T) {
	// Given: DURPDEPLOY_DB is unset (enforced by t.Setenv empty + clear)
	t.Setenv("DURPDEPLOY_DB", "")
	// When:
	dsn := loadDSN()
	// Then: the local-dev default is used, with the expected pragmas.
	if dsn != defaultDSN {
		t.Fatalf("loadDSN() = %q, want %q", dsn, defaultDSN)
	}
}

func TestLoadDSN_respectsEnvOverride(t *testing.T) {
	// Given: production-style override.
	t.Setenv("DURPDEPLOY_DB", "/var/lib/durpdeploy/durpdeploy.db")
	// When:
	dsn := loadDSN()
	// Then: the override wins.
	if !strings.HasPrefix(dsn, "/var/lib/durpdeploy/durpdeploy.db?") {
		t.Fatalf("loadDSN() = %q, want SQLite production path", dsn)
	}
}

func TestLoadAddr_defaultsAndRespectsEnvOverride(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		// Given: DURPDEPLOY_ADDR is unset.
		t.Setenv("DURPDEPLOY_ADDR", "")
		// When:
		addr := loadAddr()
		// Then: the historical listener remains unchanged.
		if addr != ":8080" {
			t.Fatalf("loadAddr() = %q, want %q", addr, ":8080")
		}
	})

	t.Run("override", func(t *testing.T) {
		// Given: a test-safe ephemeral listener override.
		t.Setenv("DURPDEPLOY_ADDR", "127.0.0.1:0")
		// When:
		addr := loadAddr()
		// Then: the override wins.
		if addr != "127.0.0.1:0" {
			t.Fatalf("loadAddr() = %q, want override", addr)
		}
	})
}

func TestRunAdminCreate_success(t *testing.T) {
	// Given: a fresh temp DB.
	dsn := tempDSN(t)
	t.Setenv("DURPDEPLOY_DB", dsn)
	email := "admin@example.com"
	password := "supersecret123" // 15 chars, >= minAdminPasswordLen

	// When: the CLI creates the admin user.
	code := runAdmin(
		[]string{"create", "--email", email, "--password", password},
	)

	// Then: exit code 0, and the user exists with role=admin and an argon2id hash.
	if code != 0 {
		t.Fatalf("runAdmin create exit code = %d, want 0", code)
	}
	ctx := context.Background()
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer conn.Close()
	repo := repository.New(conn)
	user, err := repo.Queries.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if user.Role != "admin" {
		t.Errorf("user.Role = %q, want \"admin\"", user.Role)
	}
	if user.Email != email {
		t.Errorf("user.Email = %q, want %q", user.Email, email)
	}
	if user.PasswordHash == "" || user.PasswordHash == password {
		t.Errorf("PasswordHash not stored as a hash: %q", user.PasswordHash)
	}
	if len(user.PasswordHash) < 20 {
		t.Errorf(
			"PasswordHash looks too short to be a PHC-encoded argon2id string: len=%d",
			len(user.PasswordHash),
		)
	}
}

func TestRunAdminCreate_duplicateRejected(t *testing.T) {
	// Given: an admin user already exists.
	dsn := tempDSN(t)
	t.Setenv("DURPDEPLOY_DB", dsn)
	email := "admin@example.com"
	password := "supersecret123"

	if code := runAdmin(
		[]string{"create", "--email", email, "--password", password},
	); code != 0 {
		t.Fatalf("first create exit code = %d, want 0", code)
	}

	// When: the same email is created a second time.
	code := runAdmin(
		[]string{"create", "--email", email, "--password", "differentpassword"},
	)

	// Then: it must fail non-zero with "user already exists".
	if code == 0 {
		t.Fatal(
			"second create exit code = 0, want non-zero (duplicate should be rejected)",
		)
	}
}

func TestRunAdminCreate_validationErrors(t *testing.T) {
	t.Setenv("DURPDEPLOY_DB", tempDSN(t))

	tests := []struct {
		name string
		args []string
	}{
		{
			"missing email",
			[]string{"create", "--email", "", "--password", "supersecret123"},
		},
		{
			"missing password",
			[]string{"create", "--email", "x@example.com", "--password", ""},
		},
		{
			"email without at",
			[]string{
				"create",
				"--email",
				"not-an-email",
				"--password",
				"supersecret123",
			},
		},
		{"unknown subcommand", []string{"delete", "--email", "x@example.com"}},
		{"no subcommand", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code := runAdmin(tt.args); code == 0 {
				t.Fatalf("runAdmin(%v) exit = 0, want non-zero", tt.args)
			}
		})
	}
}

func TestRunAdminCreate_shortPasswordWarnsButSucceeds(t *testing.T) {
	// Given: a password shorter than the recommended 12 chars.
	dsn := tempDSN(t)
	t.Setenv("DURPDEPLOY_DB", dsn)
	email := "short@example.com"
	password := "shortpw" // 7 chars

	// When: the CLI creates the user.
	code := runAdmin(
		[]string{"create", "--email", email, "--password", password},
	)

	// Then: it warns (stderr, not asserted here) but still exits 0 — the hard
	// requirement is non-empty, ≥12 is only a recommendation.
	if code != 0 {
		t.Fatalf(
			"runAdmin short-password exit code = %d, want 0 (warn but proceed)",
			code,
		)
	}

	// Verify the user was actually created.
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer conn.Close()
	repo := repository.New(conn)
	if _, err := repo.Queries.GetUserByEmail(
		context.Background(),
		email,
	); err != nil {
		t.Fatalf("GetUserByEmail after short-password create: %v", err)
	}
}

// Compile-time assertion that the db package symbols we depend on in tests
// match the sqlc-generated signature. Catches drift if queries/users.sql
// changes shape.
var _ = db.CreateUserParams{}

func createTestAdmin(t *testing.T, email, password string) {
	t.Helper()
	if code := runAdmin(
		[]string{"create", "--email", email, "--password", password},
	); code != 0 {
		t.Fatalf("createTestAdmin %q exit code = %d, want 0", email, code)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = origStdout
	var out bytes.Buffer
	if _, err := io.Copy(&out, r); err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return strings.TrimSpace(out.String())
}

func TestRunTokensCreate_success(t *testing.T) {
	dsn := tempDSN(t)
	t.Setenv("DURPDEPLOY_DB", dsn)
	email := "tokens@example.com"
	createTestAdmin(t, email, "supersecret123")

	out := captureStdout(t, func() {
		if code := runTokens(
			[]string{"create", "--user", email, "--name", "ci token"},
		); code != 0 {
			t.Fatalf("runTokens create exit code = %d, want 0", code)
		}
	})

	if !strings.HasPrefix(out, "ddp_pat_") {
		t.Fatalf("token plaintext does not start with ddp_pat_: %q", out)
	}

	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer conn.Close()
	repo := repository.New(conn)
	ctx := context.Background()

	rows, err := repo.Queries.ListApiTokensByUser(ctx, 1)
	if err != nil {
		t.Fatalf("ListApiTokensByUser: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("token count = %d, want 1", len(rows))
	}
	if rows[0].Name != "ci token" {
		t.Errorf("token name = %q, want %q", rows[0].Name, "ci token")
	}
	if rows[0].TokenPrefix != out[:12] {
		t.Errorf("token prefix = %q, want %q", rows[0].TokenPrefix, out[:12])
	}
}

func TestRunTokensCreate_missingFlags(t *testing.T) {
	t.Setenv("DURPDEPLOY_DB", tempDSN(t))
	cases := []struct {
		name string
		args []string
	}{
		{"no subcommand", []string{}},
		{"missing user", []string{"create", "--name", "ci token"}},
		{"missing name", []string{"create", "--user", "x@example.com"}},
		{"unknown subcommand", []string{"delete"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := runTokens(tc.args); code == 0 {
				t.Fatalf("runTokens(%v) exit = 0, want non-zero", tc.args)
			}
		})
	}
}

func TestRunTokensCreate_userNotFound(t *testing.T) {
	t.Setenv("DURPDEPLOY_DB", tempDSN(t))
	code := runTokens(
		[]string{
			"create",
			"--user",
			"missing@example.com",
			"--name",
			"ci token",
		},
	)
	if code == 0 {
		t.Fatal("runTokens create for missing user exit = 0, want non-zero")
	}
}

func TestRunTokensList_allAndByUser(t *testing.T) {
	dsn := tempDSN(t)
	t.Setenv("DURPDEPLOY_DB", dsn)
	createTestAdmin(t, "alice@example.com", "supersecret123")
	createTestAdmin(t, "bob@example.com", "supersecret123")

	if code := runTokens(
		[]string{
			"create",
			"--user",
			"alice@example.com",
			"--name",
			"alice-token",
		},
	); code != 0 {
		t.Fatalf("create alice token exit code = %d, want 0", code)
	}
	if code := runTokens(
		[]string{"create", "--user", "bob@example.com", "--name", "bob-token"},
	); code != 0 {
		t.Fatalf("create bob token exit code = %d, want 0", code)
	}

	allOut := captureStdout(t, func() {
		if code := runTokens([]string{"list"}); code != 0 {
			t.Fatalf("runTokens list exit code = %d, want 0", code)
		}
	})
	if !strings.Contains(allOut, "alice-token") ||
		!strings.Contains(allOut, "bob-token") {
		t.Fatalf("list all output missing tokens:\n%s", allOut)
	}
	if !strings.Contains(allOut, "USER_EMAIL") {
		t.Fatalf("list all output missing USER_EMAIL header:\n%s", allOut)
	}

	aliceOut := captureStdout(t, func() {
		if code := runTokens(
			[]string{"list", "--user", "alice@example.com"},
		); code != 0 {
			t.Fatalf("runTokens list --user exit code = %d, want 0", code)
		}
	})
	if !strings.Contains(aliceOut, "alice-token") {
		t.Fatalf("list --user output missing alice-token:\n%s", aliceOut)
	}
	if strings.Contains(aliceOut, "bob-token") {
		t.Fatalf("list --user output contained bob-token:\n%s", aliceOut)
	}
	if strings.Contains(aliceOut, "USER_EMAIL") {
		t.Fatalf(
			"list --user output should not contain USER_EMAIL header:\n%s",
			aliceOut,
		)
	}
}

func TestRunTokensRevoke_successAndNoMatch(t *testing.T) {
	dsn := tempDSN(t)
	t.Setenv("DURPDEPLOY_DB", dsn)
	email := "revoke@example.com"
	createTestAdmin(t, email, "supersecret123")

	var prefix string
	captureStdout(t, func() {
		if code := runTokens(
			[]string{"create", "--user", email, "--name", "revoke-me"},
		); code != 0 {
			t.Fatalf("create token exit code = %d, want 0", code)
		}
	})

	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer conn.Close()
	repo := repository.New(conn)
	ctx := context.Background()

	rows, err := repo.Queries.ListApiTokensByUser(ctx, 1)
	if err != nil {
		t.Fatalf("ListApiTokensByUser: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("token count = %d, want 1", len(rows))
	}
	prefix = rows[0].TokenPrefix

	if code := runTokens([]string{"revoke", prefix}); code != 0 {
		t.Fatalf("runTokens revoke exit code = %d, want 0", code)
	}

	rows, err = repo.Queries.ListApiTokensByUser(ctx, 1)
	if err != nil {
		t.Fatalf("ListApiTokensByUser after revoke: %v", err)
	}
	if len(rows) != 1 || !rows[0].RevokedAt.Valid {
		t.Fatalf("token was not revoked: %+v", rows[0])
	}

	if code := runTokens([]string{"revoke", "ddp_pat_0000"}); code == 0 {
		t.Fatal("runTokens revoke no-match exit = 0, want non-zero")
	}
}

func TestRecoverPendingDeployments_launchesRunnerForOrphanedDeployment(
	t *testing.T,
) {
	// Given: a deployment left in "pending" status — the goroutine that
	// the HTTP handler launched died with a previous process start.
	// This is the failure mode a container restart, OOM kill, or panic
	// leaves behind.
	dsn := tempDSN(t)
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer conn.Close()
	repo := repository.New(conn)
	ctx := context.Background()

	project, err := repo.Queries.CreateProject(ctx, db.CreateProjectParams{
		Name: "recover-proj", Description: sql.NullString{String: "x", Valid: true},
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	env, err := repo.Queries.CreateEnvironment(ctx, db.CreateEnvironmentParams{
		Name: "Dev", Description: sql.NullString{}, Tags: sql.NullString{},
	})
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "1.0.0", StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: env.ID, Status: "pending",
			StartedAt: sql.NullInt64{}, FinishedAt: sql.NullInt64{}, Forced: 0, Note: sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	// Sanity: it really is pending.
	if got, _ := repo.Queries.GetDeployment(
		ctx,
		deployment.ID,
	); got.Status != "pending" {
		t.Fatalf("precondition: status = %q, want pending", got.Status)
	}

	// When: the server starts and runs startup recovery.
	broker := runner.NewLogBroker()
	rnr := runner.New(repo, broker)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	recoverPendingDeployments(ctx, dispatch.New(repo, box, rnr), repo)

	// Then: the deployment leaves "pending" within a few seconds.
	// (Empty steps_json means the runner marks it succeeded immediately.)
	deadline := time.Now().Add(5 * time.Second)
	var finalStatus string
	for time.Now().Before(deadline) {
		got, err := repo.Queries.GetDeployment(ctx, deployment.ID)
		if err != nil {
			t.Fatalf("get deployment: %v", err)
		}
		if got.Status != "pending" {
			finalStatus = got.Status
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if finalStatus == "" {
		t.Fatalf(
			"deployment stayed in pending for 5s after recoverPendingDeployments",
		)
	}
	if finalStatus != "succeeded" {
		t.Errorf(
			"final status = %q, want succeeded (empty steps_json = no-op success)",
			finalStatus,
		)
	}
}

func TestStartupRecovery_RoutesOnlyEligiblePreStartDeployments(t *testing.T) {
	// Given: pending deployments with no dispatch, an expired pre-start claim,
	// and routes that have either started or been lost.
	dsn := tempDSN(t)
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer conn.Close()
	repo := repository.New(conn)
	ctx := context.Background()
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "recovery"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "environment"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	createPending := func() db.Deployment {
		deployment, createErr := repo.Queries.CreateDeployment(
			ctx,
			db.CreateDeploymentParams{
				ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
			},
		)
		if createErr != nil {
			t.Fatalf("create pending deployment: %v", createErr)
		}
		return deployment
	}
	noDispatch := createPending()
	expiredClaim := createPending()
	started := createPending()
	lost := createPending()
	if _, err := repo.DB.ExecContext(
		ctx,
		"INSERT INTO agent_pools (name) VALUES ('recovery-pool')",
	); err != nil {
		t.Fatalf("create agent pool: %v", err)
	}
	if _, err := repo.DB.ExecContext(ctx, `
		INSERT INTO agents (id, name, status, certificate_pem, certificate_fingerprint)
		VALUES ('agent-1', 'agent', 'active', 'certificate', ?)`, strings.Repeat("a", 64)); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	claimHash := bytes.Repeat([]byte{1}, 32)
	for _, route := range []struct {
		deploymentID int64
		state        string
	}{
		{expiredClaim.ID, "claimed"},
		{started.ID, "started"},
		{lost.ID, "lost"},
	} {
		if _, err := repo.Queries.CreateDeploymentDispatch(
			ctx,
			db.CreateDeploymentDispatchParams{
				DeploymentID: route.deploymentID,
				Mode:         "remote",
				PoolID:       sql.NullInt64{Int64: 1, Valid: true},
				Selector:     "",
				State:        route.state,
				Reason:       sql.NullString{},
			},
		); err != nil {
			t.Fatalf("create %s route: %v", route.state, err)
		}
	}
	if _, err := repo.DB.ExecContext(ctx, `
		UPDATE deployment_dispatches
		SET agent_id = 'agent-1', claim_token_hash = ?, claim_expires_at = ?
		WHERE deployment_id = ?`, claimHash, time.Now().Add(-time.Minute).Unix(), expiredClaim.ID); err != nil {
		t.Fatalf("expire claim: %v", err)
	}
	if _, err := repo.DB.ExecContext(ctx, `
		UPDATE deployment_dispatches SET started_at = ? WHERE deployment_id = ?`,
		time.Now().Add(-time.Minute).Unix(), started.ID); err != nil {
		t.Fatalf("mark started: %v", err)
	}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}

	// When: startup recovery runs through the dispatcher.
	recoverPendingDeployments(ctx, dispatch.New(repo, box, nil), repo)

	// Then: only un-routed and expired pre-start work becomes waiting.
	localRoute, err := repo.Queries.GetDeploymentDispatch(ctx, noDispatch.ID)
	if err != nil {
		t.Fatalf("get recovered local route: %v", err)
	}
	if localRoute.Mode != "local" || localRoute.State != "waiting" {
		t.Fatalf("recovered local route = %+v, want local waiting", localRoute)
	}
	expiredRoute, err := repo.Queries.GetDeploymentDispatch(
		ctx,
		expiredClaim.ID,
	)
	if err != nil {
		t.Fatalf("get expired claim route: %v", err)
	}
	if expiredRoute.State != "waiting" {
		t.Fatalf(
			"expired pre-start route = %q, want waiting",
			expiredRoute.State,
		)
	}
	for deploymentID, wantState := range map[int64]string{
		started.ID: "started",
		lost.ID:    "lost",
	} {
		route, getErr := repo.Queries.GetDeploymentDispatch(ctx, deploymentID)
		if getErr != nil {
			t.Fatalf("get preserved route %d: %v", deploymentID, getErr)
		}
		if route.State != wantState {
			t.Fatalf(
				"preserved route %d state = %q, want %q",
				deploymentID,
				route.State,
				wantState,
			)
		}
	}
}

func TestRunSecretKeyRotate_reencryptsAllRowsWithoutDataLoss(t *testing.T) {
	// Given: a DB with a variable and a release variable, both encrypted
	// with an "old" key.
	dsn := tempDSN(t)
	oldKey := make([]byte, 32)
	t.Setenv("DURPDEPLOY_DB", dsn)
	t.Setenv(secret.KeyEnvVar, base64.StdEncoding.EncodeToString(oldKey))

	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.New(conn)
	oldBox, err := secret.NewBox(oldKey)
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	repo.SetSecretBox(oldBox)
	ctx := context.Background()

	project, err := repo.Queries.CreateProject(ctx, db.CreateProjectParams{
		Name: "rotate-proj",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	const plaintext = "rotate-me-secret"
	variable, err := repo.CreateVariable(ctx, db.CreateVariableParams{
		ProjectID: project.ID,
		Name:      "TOKEN",
		Value:     sql.NullString{String: plaintext, Valid: true},
		Secret:    1,
	})
	if err != nil {
		t.Fatalf("CreateVariable: %v", err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "1.0.0", StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	encValue, err := repo.EncryptValue(
		sql.NullString{String: plaintext, Valid: true},
	)
	if err != nil {
		t.Fatalf("EncryptValue: %v", err)
	}
	releaseVar, err := repo.Queries.CreateReleaseVariable(
		ctx,
		db.CreateReleaseVariableParams{
			ReleaseID: release.ID,
			Name:      "TOKEN",
			Value:     encValue,
			Secret:    1,
		},
	)
	if err != nil {
		t.Fatalf("CreateReleaseVariable: %v", err)
	}
	conn.Close()

	// When: the key is rotated. Capture stdout to recover the newly
	// generated key the operator would install.
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	code := runSecretKey([]string{"rotate"})
	w.Close()
	os.Stdout = origStdout
	var out bytes.Buffer
	if _, err := io.Copy(&out, r); err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	if code != 0 {
		t.Fatalf("runSecretKey rotate exit code = %d, want 0", code)
	}

	newKeyB64 := extractNewKey(t, out.String())
	newKeyRaw, err := base64.StdEncoding.DecodeString(newKeyB64)
	if err != nil {
		t.Fatalf("decode printed new key: %v", err)
	}
	newBox, err := secret.NewBox(newKeyRaw)
	if err != nil {
		t.Fatalf("NewBox(new key): %v", err)
	}

	// Then: the raw DB row for both tables is no longer decryptable with
	// the old key (it was re-encrypted)...
	conn2, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer conn2.Close()
	q := db.New(conn2)

	rawVar, err := q.GetVariable(ctx, variable.ID)
	if err != nil {
		t.Fatalf("GetVariable: %v", err)
	}
	if _, err := oldBox.Decrypt(rawVar.Value.String); err == nil {
		t.Fatal("expected old key to no longer decrypt the rotated variable")
	}

	rawReleaseVar, err := q.GetReleaseVariable(ctx, releaseVar.ID)
	if err != nil {
		t.Fatalf("GetReleaseVariable: %v", err)
	}
	if _, err := oldBox.Decrypt(rawReleaseVar.Value.String); err == nil {
		t.Fatal(
			"expected old key to no longer decrypt the rotated release variable",
		)
	}

	// ...but no secret was lost: the newly generated key decrypts both
	// rows back to the exact original plaintext.
	gotVar, err := newBox.Decrypt(rawVar.Value.String)
	if err != nil {
		t.Fatalf("decrypt rotated variable with new key: %v", err)
	}
	if gotVar != plaintext {
		t.Fatalf("rotated variable value = %q, want %q", gotVar, plaintext)
	}
	gotReleaseVar, err := newBox.Decrypt(rawReleaseVar.Value.String)
	if err != nil {
		t.Fatalf("decrypt rotated release variable with new key: %v", err)
	}
	if gotReleaseVar != plaintext {
		t.Fatalf(
			"rotated release variable value = %q, want %q",
			gotReleaseVar,
			plaintext,
		)
	}
}

// extractNewKey pulls the base64 key line out of runSecretKey's stdout
// output (the line immediately following the "New key" banner).
func extractNewKey(t *testing.T, output string) string {
	t.Helper()
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		if strings.Contains(line, "New key") && i+1 < len(lines) {
			return strings.TrimSpace(lines[i+1])
		}
	}
	t.Fatalf("could not find new key in rotate output:\n%s", output)
	return ""
}

func TestPruneAuditLogs_preservesLiveDeploymentAndReleaseRows(t *testing.T) {
	// Given: a live deployment and release, plus three audit rows backdated
	// well past the prune cutoff — two tied to live entities (must survive)
	// and one tied to a dead project ID (must be deleted).
	dsn := tempDSN(t)
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer conn.Close()
	repo := repository.New(conn)
	ctx := context.Background()

	project, err := repo.Queries.CreateProject(ctx, db.CreateProjectParams{
		Name: "prune-proj",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	env, err := repo.Queries.CreateEnvironment(ctx, db.CreateEnvironmentParams{
		Name: "Dev", Description: sql.NullString{}, Tags: sql.NullString{},
	})
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "1.0.0", StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: env.ID, Status: "succeeded",
			StartedAt: sql.NullInt64{}, FinishedAt: sql.NullInt64{}, Forced: 0, Note: sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	// Insert three audit rows, then backdate created_at to well before now.
	oldTS := time.Now().Unix() - 30*24*60*60 // 30 days ago
	rows := []struct {
		action     string
		entityType string
		entityID   sql.NullInt64
	}{
		{
			"create_deployment",
			"deployment",
			sql.NullInt64{Int64: deployment.ID, Valid: true},
		},
		{
			"create_release",
			"release",
			sql.NullInt64{Int64: release.ID, Valid: true},
		},
		{"update_project", "project", sql.NullInt64{Int64: 99999, Valid: true}},
	}
	var ids []int64
	for _, r := range rows {
		a, err := repo.Queries.CreateAuditLog(ctx, db.CreateAuditLogParams{
			UserID:     sql.NullInt64{},
			Action:     r.action,
			EntityType: r.entityType,
			EntityID:   r.entityID,
			Details:    sql.NullString{},
		})
		if err != nil {
			t.Fatalf("CreateAuditLog %s: %v", r.action, err)
		}
		ids = append(ids, a.ID)
	}
	for _, id := range ids {
		if _, err := conn.ExecContext(
			ctx, "UPDATE audit_log SET created_at = ? WHERE id = ?", oldTS, id,
		); err != nil {
			t.Fatalf("backdate audit row %d: %v", id, err)
		}
	}

	// When: prune everything older than now.
	if err := repo.Queries.PruneAuditLogs(ctx, time.Now().Unix()); err != nil {
		t.Fatalf("PruneAuditLogs: %v", err)
	}

	// Then: the two live-entity rows survive, the dead-entity row is gone.
	for i, id := range ids {
		var exists int
		err := conn.QueryRowContext(
			ctx, "SELECT 1 FROM audit_log WHERE id = ?", id,
		).Scan(&exists)
		if i < 2 {
			if err != nil {
				t.Errorf(
					"live-entity audit row %d (entity=%s) was pruned, want preserved: %v",
					id,
					rows[i].entityType,
					err,
				)
			}
		} else {
			if err == nil {
				t.Errorf(
					"dead-entity audit row %d (entity=%s id=%d) survived, want pruned",
					id,
					rows[i].entityType,
					rows[i].entityID.Int64,
				)
			}
		}
	}
}

func TestRecoverPendingDeployments_noopWhenNonePending(t *testing.T) {
	// Given: a fresh DB with no deployments.
	dsn := tempDSN(t)
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer conn.Close()
	repo := repository.New(conn)
	ctx := context.Background()

	// When: recovery runs.
	broker := runner.NewLogBroker()
	rnr := runner.New(repo, broker)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	recoverPendingDeployments(ctx, dispatch.New(repo, box, rnr), repo)

	// Then: no panic, no error, no goroutine spawned. We can't directly
	// assert "no goroutine" but the function returning cleanly with no
	// rows to iterate is the observable signal. Wait briefly to be sure
	// nothing async was kicked off.
	time.Sleep(100 * time.Millisecond)
}
