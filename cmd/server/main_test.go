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
	if dsn != "/var/lib/durpdeploy/durpdeploy.db" {
		t.Fatalf("loadDSN() = %q, want production path", dsn)
	}
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
	recoverPendingDeployments(ctx, rnr, repo)

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
	encValue, err := repo.EncryptValue(sql.NullString{String: plaintext, Valid: true})
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
		t.Fatal("expected old key to no longer decrypt the rotated release variable")
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
		t.Fatalf("rotated release variable value = %q, want %q", gotReleaseVar, plaintext)
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
	recoverPendingDeployments(ctx, rnr, repo)

	// Then: no panic, no error, no goroutine spawned. We can't directly
	// assert "no goroutine" but the function returning cleanly with no
	// rows to iterate is the observable signal. Wait briefly to be sure
	// nothing async was kicked off.
	time.Sleep(100 * time.Millisecond)
}
