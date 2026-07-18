package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/scheduler"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"
)

// defaultDSN mirrors the historical hardcoded DSN. Production overrides it
// via DURPDEPLOY_DB (see loadDSN).
const defaultDSN = "durpdeploy.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

func main() {
	// ponytail: subcommand dispatcher in main() instead of a CLI framework
	// (cobra/urfave). Three subcommands do not justify a dependency; if the
	// surface grows past ~6 subcommands, pull in cobra + huh.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "admin":
			os.Exit(runAdmin(os.Args[2:]))
		case "secret-key":
			os.Exit(runSecretKey(os.Args[2:]))
		case "version", "--version", "-v":
			fmt.Println("durpdeploy dev")
			os.Exit(0)
		case "help", "--help", "-h":
			fmt.Println(
				"Usage: durpdeploy [admin create --email X --password Y] [secret-key rotate [--plaintext]] [version] [help]",
			)
			fmt.Println("With no subcommand, starts the HTTP server.")
			os.Exit(0)
		}
	}
	runServer()
}

// loadDSN returns the SQLite DSN from DURPDEPLOY_DB, falling back to the
// local-dev default. Both the server and the admin CLI route through here so
// they always agree on which database is in use.
func loadDSN() string {
	if dsn := os.Getenv("DURPDEPLOY_DB"); dsn != "" {
		return dsn
	}
	return defaultDSN
}

// runServer starts the HTTP server. This is the body of the former main().
func runServer() {
	// Registered before anything else (migrations, recovery) so a signal
	// arriving during startup is queued on the channel instead of being
	// handled by Go's default disposition (immediate process termination),
	// which would skip the KillAll cleanup below.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	slog.SetDefault(
		slog.New(
			slog.NewJSONHandler(
				os.Stdout,
				&slog.HandlerOptions{Level: slog.LevelInfo},
			),
		),
	)

	key, err := secret.LoadKey()
	if err != nil {
		log.Fatalf("secret key: %v", err)
	}
	box, err := secret.NewBox(key)
	if err != nil {
		log.Fatalf("secret key: %v", err)
	}

	dbConn, err := migrate.Run(loadDSN())
	if err != nil {
		log.Fatalf("migration failed: %v", err)
	}
	defer dbConn.Close()
	slog.Info("database ready")

	repo := repository.New(dbConn)
	repo.SetSecretBox(box)
	broker := runner.NewLogBroker()
	rnr := runner.New(repo, broker)
	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	sched := scheduler.New(repo, rnr)
	ctx, cancel := context.WithCancel(context.Background())
	sched.Start(ctx)
	defer sched.Stop()
	defer cancel()
	authHandler := handler.NewAuthHandler(repo)
	r := server.NewRouter(repo, rnr, parser, authHandler)

	// Recover deployments that were created but never picked up by a
	// runner goroutine (process restarted, container OOM, manual kill,
	// etc.). Without this, a deployment sitting in "pending" stays there
	// forever — the HTTP handler launched the runner as a goroutine and
	// that goroutine dies with the process.
	recoverPendingDeployments(ctx, rnr, repo)

	srv := &http.Server{Addr: ":8080", Handler: r}

	// Graceful shutdown: on SIGINT/SIGTERM, stop accepting new connections
	// and SIGKILL any in-flight deploy step's process group so a restart
	// never leaves an orphaned bash tree running as the server's user
	// (P1-3). The runner keeps running in the background (its context is
	// context.Background(), not tied to srv's lifetime) — KillAll is what
	// actually stops the child processes. The WaitGroup ensures runServer
	// (and thus main) does not return until KillAll has finished, so the
	// process doesn't exit mid-cleanup and leave orphaned children behind.
	var shutdownWG sync.WaitGroup
	shutdownWG.Add(1)
	go func() {
		defer shutdownWG.Done()
		<-stop
		slog.Info("shutdown signal received, draining")
		shutdownCtx, shutdownCancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
		rnr.KillAll()
	}()

	slog.Info("server starting", "addr", ":8080")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server failed: %v", err)
	}
	shutdownWG.Wait()
}

// recoverPendingDeployments scans for deployments left in "pending"
// status and re-launches their runners. Called once at server startup
// (see runServer).
//
// ponytail: the runner.Run call does NOT use a SELECT ... FOR UPDATE
// or any atomic claim — the runner transitions status to "running"
// on its first DB write (internal/runner/runner.go:188). If two
// goroutines ever saw the same pending row (recovery at startup + a
// concurrent HTTP create), both would transition it to "running" and
// run the steps. In practice this can't happen — recovery runs once
// at boot, before any HTTP handler is reachable — but the race
// remains in the contract. Fix with a conditional UPDATE (WHERE
// status='pending') if the startup window ever overlaps with traffic.
func recoverPendingDeployments(
	ctx context.Context,
	rnr *runner.DeploymentRunner,
	repo *repository.Repository,
) {
	pending, err := repo.Queries.ListPendingDeployments(ctx)
	if err != nil {
		slog.Error(
			"startup recovery: list pending deployments failed",
			"err",
			err,
		)
		return
	}
	if len(pending) == 0 {
		return
	}
	slog.Info(
		"startup recovery: re-launching runners for pending deployments",
		"count",
		len(pending),
	)
	for _, d := range pending {
		d := d // capture
		slog.Info(
			"startup recovery: re-launching",
			"deployment_id",
			d.ID,
			"project",
			d.ProjectName,
			"env",
			d.EnvironmentName,
		)
		go rnr.Run(context.Background(), d.ID, d.ReleaseID, d.EnvironmentID)
	}
}

// minAdminPasswordLen is the recommended minimum. We warn below this but do
// not hard-reject — an operator restoring a known-good password may legitimately
// need to enter a shorter one. Non-empty is the only hard requirement.
const minAdminPasswordLen = 12

// runAdmin implements `durpdeploy admin create --email X --password Y`.
// It opens the same database the server uses (via loadDSN), runs migrations
// so a fresh install works without the server having run first, and creates
// an admin user with an argon2id password hash. Returns the process exit code.
//
// ponytail: the CLI runs migrate.Run so a fresh VM can create the admin user
// before the server is started. This couples the CLI to the migrate package,
// which is fine — the CLI owns "bootstrap the DB" semantics.
func runAdmin(args []string) int {
	fs := flag.NewFlagSet("admin", flag.ExitOnError)
	_ = fs.Parse(args) // consumes the leading "admin" already stripped by main

	if fs.NArg() == 0 {
		fmt.Fprintln(
			os.Stderr,
			"Usage: durpdeploy admin create --email X --password Y",
		)
		return 1
	}
	if fs.Arg(0) != "create" {
		fmt.Fprintf(
			os.Stderr,
			"unknown admin subcommand %q; only \"create\" is supported\n",
			fs.Arg(0),
		)
		return 1
	}

	createCmd := flag.NewFlagSet("create", flag.ExitOnError)
	emailPtr := createCmd.String("email", "", "admin email")
	passwordPtr := createCmd.String("password", "", "admin password")
	if err := createCmd.Parse(fs.Args()[1:]); err != nil {
		return 1
	}
	email, password := *emailPtr, *passwordPtr

	// ponytail: strings.Contains(email, "@") is not RFC 5322 but is the right
	// tradeoff for a CLI bootstrap command — full RFC validation belongs at the
	// HTTP boundary, not here. Catches the obvious typos (missing @).
	if email == "" {
		fmt.Fprintln(os.Stderr, "error: --email is required")
		return 1
	}
	if !strings.Contains(email, "@") {
		fmt.Fprintf(
			os.Stderr,
			"error: --email %q does not look like an email (missing '@')\n",
			email,
		)
		return 1
	}
	if password == "" {
		fmt.Fprintln(os.Stderr, "error: --password is required")
		return 1
	}
	if len(password) < minAdminPasswordLen {
		fmt.Fprintf(
			os.Stderr,
			"warning: password is %d chars, shorter than recommended %d; proceeding\n",
			len(password),
			minAdminPasswordLen,
		)
	}

	ctx := context.Background()
	dbConn, err := migrate.Run(loadDSN())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open database: %v\n", err)
		return 1
	}
	defer dbConn.Close()

	repo := repository.New(dbConn)

	if _, err := repo.Queries.GetUserByEmail(ctx, email); err == nil {
		fmt.Fprintf(os.Stderr, "error: user already exists: %s\n", email)
		return 1
	} else if !errors.Is(err, sql.ErrNoRows) {
		fmt.Fprintf(os.Stderr, "error: lookup user: %v\n", err)
		return 1
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: hash password: %v\n", err)
		return 1
	}

	if _, err := repo.Queries.CreateUser(ctx, db.CreateUserParams{
		Email:        email,
		PasswordHash: hash,
		Name:         email,
		Role:         "admin",
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error: create user: %v\n", err)
		return 1
	}

	fmt.Printf("Created admin user: %s\n", email)
	return 0
}

// runSecretKey implements `durpdeploy secret-key rotate [--plaintext]`. It
// generates a fresh 32-byte key, decrypts every variables/release_variables
// row with the currently configured key (secret.LoadKey), and re-encrypts it
// with the new one — all inside a single transaction, so a crash mid-rotation
// leaves the DB entirely on the old key, never half-migrated. The new key
// is printed to stdout; the operator must install it (file or env) and
// restart the server before the old key can be discarded. See
// docs/security.md for the full runbook.
//
// --plaintext migrates a pre-P1-2 database whose variables/release_variables
// values were stored unencrypted: it skips the oldBox.Decrypt step and
// treats the stored value itself as the plaintext to encrypt. No oldKey is
// required or loaded in that mode.
func runSecretKey(args []string) int {
	fs := flag.NewFlagSet("secret-key", flag.ExitOnError)
	plaintext := fs.Bool(
		"plaintext",
		false,
		"treat existing values as unencrypted plaintext (first-time migration to encryption at rest)",
	)
	_ = fs.Parse(args)

	if fs.NArg() == 0 || fs.Arg(0) != "rotate" {
		fmt.Fprintln(os.Stderr, "Usage: durpdeploy secret-key rotate [--plaintext]")
		return 1
	}

	var oldBox *secret.Box
	if !*plaintext {
		oldKey, err := secret.LoadKey()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: load current secret key: %v\n", err)
			return 1
		}
		oldBox, err = secret.NewBox(oldKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}

	newKey := make([]byte, 32)
	if _, err := rand.Read(newKey); err != nil {
		fmt.Fprintf(os.Stderr, "error: generate new key: %v\n", err)
		return 1
	}
	newBox, err := secret.NewBox(newKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	ctx := context.Background()
	dbConn, err := migrate.Run(loadDSN())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open database: %v\n", err)
		return 1
	}
	defer dbConn.Close()

	q := db.New(dbConn)

	tx, err := dbConn.BeginTx(ctx, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: begin transaction: %v\n", err)
		return 1
	}
	defer tx.Rollback()
	qtx := q.WithTx(tx)

	vars, err := qtx.ListAllVariables(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: list variables: %v\n", err)
		return 1
	}
	for _, v := range vars {
		if !v.Value.Valid || v.Value.String == "" {
			continue
		}
		plain := v.Value.String
		if !*plaintext {
			var err error
			plain, err = oldBox.Decrypt(v.Value.String)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: decrypt variable %d: %v\n", v.ID, err)
				return 1
			}
		}
		reenc, err := newBox.Encrypt(plain)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: encrypt variable %d: %v\n", v.ID, err)
			return 1
		}
		if err := qtx.UpdateVariableValue(ctx, db.UpdateVariableValueParams{
			Value: sql.NullString{String: reenc, Valid: true},
			ID:    v.ID,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "error: update variable %d: %v\n", v.ID, err)
			return 1
		}
	}

	relVars, err := qtx.ListAllReleaseVariables(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: list release variables: %v\n", err)
		return 1
	}
	for _, v := range relVars {
		if !v.Value.Valid || v.Value.String == "" {
			continue
		}
		plain := v.Value.String
		if !*plaintext {
			var err error
			plain, err = oldBox.Decrypt(v.Value.String)
			if err != nil {
				fmt.Fprintf(
					os.Stderr,
					"error: decrypt release variable %d: %v\n",
					v.ID, err,
				)
				return 1
			}
		}
		reenc, err := newBox.Encrypt(plain)
		if err != nil {
			fmt.Fprintf(
				os.Stderr,
				"error: encrypt release variable %d: %v\n",
				v.ID, err,
			)
			return 1
		}
		if err := qtx.UpdateReleaseVariableValue(
			ctx,
			db.UpdateReleaseVariableValueParams{
				Value: sql.NullString{String: reenc, Valid: true},
				ID:    v.ID,
			},
		); err != nil {
			fmt.Fprintf(
				os.Stderr,
				"error: update release variable %d: %v\n",
				v.ID, err,
			)
			return 1
		}
	}

	if err := tx.Commit(); err != nil {
		fmt.Fprintf(os.Stderr, "error: commit: %v\n", err)
		return 1
	}

	fmt.Printf(
		"Rotated %d variable(s) and %d release variable(s) to a new key.\n",
		len(vars), len(relVars),
	)
	fmt.Println()
	fmt.Println("New key (base64) — install it BEFORE restarting the server:")
	fmt.Println(base64.StdEncoding.EncodeToString(newKey))
	fmt.Println()
	fmt.Println("Either write it to /etc/durpdeploy/key (0600, owned by the")
	fmt.Println("durpdeploy user) or set DURPDEPLOY_SECRET_KEY to the value")
	fmt.Println("above, then restart durpdeploy. The old key must not be")
	fmt.Println("reused: every row above was just re-encrypted with the new one.")
	return 0
}
