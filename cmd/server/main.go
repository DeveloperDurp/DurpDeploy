package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/scheduler"
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
		case "version", "--version", "-v":
			fmt.Println("durpdeploy dev")
			os.Exit(0)
		case "help", "--help", "-h":
			fmt.Println(
				"Usage: durpdeploy [admin create --email X --password Y] [version] [help]",
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
	slog.SetDefault(
		slog.New(
			slog.NewJSONHandler(
				os.Stdout,
				&slog.HandlerOptions{Level: slog.LevelInfo},
			),
		),
	)

	dbConn, err := migrate.Run(loadDSN())
	if err != nil {
		log.Fatalf("migration failed: %v", err)
	}
	defer dbConn.Close()
	slog.Info("database ready")

	repo := repository.New(dbConn)
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

	slog.Info("server starting", "addr", ":8080")
	if err := http.ListenAndServe(":8080", r); err != nil {
		log.Fatalf("server failed: %v", err)
	}
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
