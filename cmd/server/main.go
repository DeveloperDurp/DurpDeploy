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
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"durpdeploy/internal/agentpairing"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/events"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/maintenance"
	"durpdeploy/internal/mfa"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/notify"
	"durpdeploy/internal/oidc"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/scheduler"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

// defaultDSN mirrors the historical hardcoded DSN. Production overrides it
// via DURPDEPLOY_DB (see loadDSN).
const defaultDSN = "durpdeploy.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"

var newDeploymentRunner = runner.New

type oidcServicesConfig struct {
	Config       oidc.Config
	Repository   *repository.Repository
	Box          *secret.Box
	HTTPClient   *http.Client
	CookieSecure bool
	Now          func() time.Time
}

type oidcServices struct {
	enabled      bool
	provider     *oidc.Provider
	transactions *oidc.TransactionStore
}

func newOIDCServices(config oidcServicesConfig) (oidcServices, error) {
	if !config.Config.Enabled {
		return oidcServices{}, nil
	}

	codec, err := oidc.NewTransactionCookieCodec(
		config.Box,
		oidc.TransactionCookieConfig{
			Secure: config.CookieSecure,
			Now:    config.Now,
		},
	)
	if err != nil {
		return oidcServices{}, fmt.Errorf(
			"new OIDC transaction cookie: %w",
			err,
		)
	}
	transactions, err := oidc.NewTransactionStore(oidc.TransactionStoreOptions{
		Repository: config.Repository, CookieCodec: codec,
	})
	if err != nil {
		return oidcServices{}, fmt.Errorf("new OIDC transaction store: %w", err)
	}
	provider, err := oidc.NewProvider(oidc.ProviderOptions{
		Config:     config.Config,
		HTTPClient: config.HTTPClient,
		Now:        config.Now,
	})
	if err != nil {
		return oidcServices{}, fmt.Errorf("new OIDC provider: %w", err)
	}
	return oidcServices{
		enabled:      true,
		provider:     provider,
		transactions: transactions,
	}, nil
}

func main() {
	// ponytail: subcommand dispatcher in main() instead of a CLI framework
	// (cobra/urfave). Three subcommands do not justify a dependency; if the
	// surface grows past ~6 subcommands, pull in cobra + huh.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "admin":
			os.Exit(runAdmin(os.Args[2:]))
		case "audit":
			os.Exit(runAudit(os.Args[2:]))
		case "secret-key":
			os.Exit(runSecretKey(os.Args[2:]))
		case "tokens":
			os.Exit(runTokens(os.Args[2:]))
		case "version", "--version", "-v":
			fmt.Println("durpdeploy dev")
			os.Exit(0)
		case "help", "--help", "-h":
			fmt.Println(
				"Usage: durpdeploy [admin create --email X --password Y] [audit prune [--days N]] [secret-key rotate [--plaintext]] [tokens create/list/revoke] [version] [help]",
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
		if strings.HasPrefix(dsn, "postgres://") ||
			strings.HasPrefix(dsn, "postgresql://") ||
			strings.HasPrefix(dsn, "sqlserver://") {
			return dsn
		}
		separator := "?"
		if strings.Contains(dsn, "?") {
			separator = "&"
		}
		return dsn + separator +
			"_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&" +
			"_pragma=busy_timeout(5000)&_txlock=immediate"
	}

	return defaultDSN
}

// loadAddr returns the HTTP listen address. The default preserves the
// historical :8080 behavior; tests can bind 127.0.0.1:0-style addresses by
// setting DURPDEPLOY_ADDR.
func loadAddr() string {
	if addr := os.Getenv("DURPDEPLOY_ADDR"); addr != "" {
		return addr
	}
	return ":8080"
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

	mfaConfig, err := mfa.LoadConfig()
	if err != nil {
		log.Fatalf("mfa config: %v", err)
	}
	oidcConfig, err := oidc.LoadConfig()
	if err != nil {
		log.Fatalf("oidc config: %v", err)
	}
	slog.Info(
		"MFA configuration loaded",
		"webauthn_enabled", mfaConfig.WebAuthn.Enabled,
		"cookie_secure", mfaConfig.CookieSecure,
	)

	key, err := secret.LoadKey()
	if err != nil {
		log.Fatalf("secret key: %v", err)
	}
	box, err := secret.NewBox(key)
	if err != nil {
		log.Fatalf("secret key: %v", err)
	}
	mfaService := mfa.NewService(mfaConfig, box)

	dbConn, err := migrate.Run(loadDSN())
	if err != nil {
		log.Fatalf("migration failed: %v", err)
	}
	defer dbConn.Close()
	slog.Info("database ready")

	repo := repository.New(dbConn)
	repo.SetSecretBox(box)
	broker := runner.NewLogBroker()
	rnr := newDeploymentRunner(repo, broker)
	dispatcher := dispatch.New(repo, box, rnr)

	// Event bus for Slack/email/Gotify notifications on deployment
	// start/success/failure (Stage 3). Every event is recorded to
	// notification_events regardless of whether any notifier is actually
	// configured, so /admin/notifications is useful even with nothing set up.
	bus := events.NewBus(repo)
	bus.Register(notify.NewSlackNotifier())
	bus.Register(notify.NewEmailNotifier(notify.LoadSMTPConfigFromEnv()))
	bus.Register(notify.NewGotifyNotifier())
	bus.Register(notify.NewDiscordNotifier())
	rnr.SetEventBus(bus)
	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	sched := scheduler.New(repo, dispatcher)
	ctx, cancel := context.WithCancel(context.Background())
	maintenance.StartLitestreamCheck(ctx, bus)
	sched.Start(ctx)
	defer sched.Stop()
	defer cancel()
	authHandler := handler.NewAuthHandler(repo)
	authHandler.SetMFAService(mfaService)
	authHandler.SetOIDCDisplayName(oidcConfig.DisplayName)
	oidcServices, err := newOIDCServices(oidcServicesConfig{
		Config:       oidcConfig,
		Repository:   repo,
		Box:          box,
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
		CookieSecure: mfaConfig.CookieSecure,
		Now:          time.Now,
	})
	if err != nil {
		log.Fatalf("OIDC service: %v", err)
	}
	if oidcServices.enabled {
		authHandler.SetOIDCLogin(
			oidcServices.provider,
			oidcServices.transactions,
		)
		issuerURL, err := url.Parse(oidcConfig.Issuer)
		if err != nil {
			log.Fatalf("OIDC issuer: %v", err)
		}
		slog.Info(
			"OIDC enabled",
			"enabled", true,
			"display_name", oidcConfig.DisplayName,
			"issuer_host", issuerURL.Hostname(),
		)
	}
	agentConfig, agentEnabled, err := loadAgentListenerConfig()
	if err != nil {
		log.Fatalf("agent listener config: %v", err)
	}
	var agents *agentListener
	var pairer *agentpairing.Server
	if agentEnabled {
		agents, err = startAgentListener(agentConfig, agentListenerDependencies{
			repo: repo, box: box, broker: broker, bus: bus,
		})
		if err != nil {
			log.Fatalf("agent listener: %v", err)
		}
		pullEndpoint, parseErr := agentproto.ParsePullEndpoint(
			agentConfig.publicURL,
		)
		if parseErr != nil {
			log.Fatalf("agent pull endpoint: %v", parseErr)
		}
		pairer, err = agentpairing.NewServer(pullEndpoint, agents.identity)
		if err != nil {
			log.Fatalf("agent pairer: %v", err)
		}
		agents.startMaintenance(ctx)
		slog.Info("agent listener starting", "addr", agentConfig.addr)
	}
	r := server.NewRouterWithAgentPairer(
		repo,
		rnr,
		dispatcher,
		parser,
		authHandler,
		pairer,
		oidcServices.enabled,
	)

	// Recover deployments that were created but never picked up by a
	// runner goroutine (process restarted, container OOM, manual kill,
	// etc.). Without this, a deployment sitting in "pending" stays there
	// forever — the HTTP handler launched the runner as a goroutine and
	// that goroutine dies with the process.
	recoverPendingDeployments(ctx, dispatcher, repo)

	addr := loadAddr()
	srv := &http.Server{Addr: addr, Handler: r}

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
		cancel()
		if agents != nil {
			if err := agents.shutdown(shutdownCtx); err != nil {
				slog.Error("agent listener shutdown failed", "err", err)
			}
		}
		_ = srv.Shutdown(shutdownCtx)
		rnr.KillAll()
	}()

	slog.Info("server starting", "addr", addr)
	if err := srv.ListenAndServe(); err != nil &&
		!errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server failed: %v", err)
	}
	shutdownWG.Wait()
}

func recoverPendingDeployments(
	ctx context.Context,
	dispatcher *dispatch.Dispatcher,
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
	now := time.Now().Unix()
	for _, d := range pending {
		route, err := repo.Queries.GetDeploymentDispatch(ctx, d.ID)
		if errors.Is(err, sql.ErrNoRows) {
			recoverDeployment(ctx, dispatcher, d.ID)
			continue
		}
		if err != nil {
			slog.Error(
				"startup recovery: get deployment dispatch failed",
				"deployment_id",
				d.ID,
				"err",
				err,
			)
			continue
		}
		if route.Mode != "remote" || route.State != "claimed" ||
			route.StartedAt.Valid || !route.ClaimExpiresAt.Valid ||
			route.ClaimExpiresAt.Int64 > now {
			continue
		}
		if _, err := repo.Queries.TransitionDeploymentDispatch(
			ctx,
			db.TransitionDeploymentDispatchParams{
				NextState: "waiting",
				Reason: sql.NullString{
					String: "claim expired before start",
					Valid:  true,
				},
				FinishedAt:     sql.NullInt64{},
				DeploymentID:   d.ID,
				AgentID:        route.AgentID,
				ClaimTokenHash: route.ClaimTokenHash,
				CurrentState:   "claimed",
			},
		); err != nil {
			slog.Error(
				"startup recovery: release expired deployment claim failed",
				"deployment_id",
				d.ID,
				"err",
				err,
			)
			continue
		}
		recoverDeployment(ctx, dispatcher, d.ID)
	}
}

func recoverDeployment(
	ctx context.Context,
	dispatcher *dispatch.Dispatcher,
	deploymentID int64,
) {
	if err := dispatcher.Dispatch(ctx, deploymentID); err != nil {
		slog.Error(
			"startup recovery: dispatch deployment failed",
			"deployment_id",
			deploymentID,
			"err",
			err,
		)
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

// defaultAuditRetentionDays is the fallback retention period for
// `audit prune` when neither --days nor DURPDEPLOY_AUDIT_RETENTION_DAYS is
// set. 180 days keeps roughly half a year of audit history by default.
const defaultAuditRetentionDays = 180

// runAudit implements `durpdeploy audit prune [--days N]`.
// It opens the same database the server uses and deletes audit_log rows
// older than the specified retention period. The retention days resolve in
// this order: --days flag (if > 0), DURPDEPLOY_AUDIT_RETENTION_DAYS env (if
// set and > 0), then defaultAuditRetentionDays.
func runAudit(args []string) int {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	_ = fs.Parse(args)

	if fs.NArg() == 0 {
		fmt.Fprintln(
			os.Stderr,
			"Usage: durpdeploy audit prune [--days N]   (DURPDEPLOY_AUDIT_RETENTION_DAYS env, default 180)",
		)
		return 1
	}

	if fs.Arg(0) != "prune" {
		fmt.Fprintf(
			os.Stderr,
			"unknown audit subcommand %q; only \"prune\" is supported\n",
			fs.Arg(0),
		)
		return 1
	}

	pruneCmd := flag.NewFlagSet("prune", flag.ExitOnError)
	daysPtr := pruneCmd.Int(
		"days",
		0,
		"retention in days (overrides DURPDEPLOY_AUDIT_RETENTION_DAYS; default 180)",
	)
	if err := pruneCmd.Parse(fs.Args()[1:]); err != nil {
		return 1
	}

	days := *daysPtr
	daysExplicit := false
	pruneCmd.Visit(func(f *flag.Flag) {
		if f.Name == "days" {
			daysExplicit = true
		}
	})
	if days <= 0 {
		if daysExplicit {
			fmt.Fprintln(os.Stderr, "error: --days must be greater than 0")
			return 1
		}
		// Fall back to env, then to the hard-coded default.
		if envDays := os.Getenv(
			"DURPDEPLOY_AUDIT_RETENTION_DAYS",
		); envDays != "" {
			n, err := fmt.Sscanf(envDays, "%d", &days)
			if err != nil || n != 1 || days <= 0 {
				fmt.Fprintf(
					os.Stderr,
					"error: DURPDEPLOY_AUDIT_RETENTION_DAYS=%q is not a positive integer\n",
					envDays,
				)
				return 1
			}
		} else {
			days = defaultAuditRetentionDays
		}
	}

	ctx := context.Background()
	dbConn, err := migrate.Run(loadDSN())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open database: %v\n", err)
		return 1
	}
	defer dbConn.Close()

	repo := repository.New(dbConn)
	cutoff := time.Now().AddDate(0, 0, -days).Unix()

	if err := repo.Queries.PruneAuditLogs(ctx, cutoff); err != nil {
		fmt.Fprintf(os.Stderr, "error: prune audit logs: %v\n", err)
		return 1
	}

	fmt.Printf(
		"Pruned audit logs older than %d days (cutoff: %s)\n",
		days,
		time.Unix(cutoff, 0).Format(time.RFC3339),
	)
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
		fmt.Fprintln(
			os.Stderr,
			"Usage: durpdeploy secret-key rotate [--plaintext]",
		)
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
				fmt.Fprintf(
					os.Stderr,
					"error: decrypt variable %d: %v\n",
					v.ID,
					err,
				)
				return 1
			}
		}
		reenc, err := newBox.Encrypt(plain)
		if err != nil {
			fmt.Fprintf(
				os.Stderr,
				"error: encrypt variable %d: %v\n",
				v.ID,
				err,
			)
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
	fmt.Println(
		"reused: every row above was just re-encrypted with the new one.",
	)
	return 0
}

// runTokens implements `durpdeploy tokens create/list/revoke`.
func runTokens(args []string) int {
	fs := flag.NewFlagSet("tokens", flag.ExitOnError)
	_ = fs.Parse(args)

	if fs.NArg() == 0 {
		fmt.Fprintln(
			os.Stderr,
			"Usage: durpdeploy tokens create --user <email> --name <label> | durpdeploy tokens list [--user <email>] | durpdeploy tokens revoke <token_prefix>",
		)
		return 1
	}

	ctx := context.Background()
	dbConn, err := migrate.Run(loadDSN())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open database: %v\n", err)
		return 1
	}
	defer dbConn.Close()
	repo := repository.New(dbConn)

	fmtTime := func(ts int64) string {
		return time.Unix(ts, 0).Format(time.RFC3339)
	}
	fmtNullTime := func(n sql.NullInt64) string {
		if !n.Valid {
			return ""
		}
		return time.Unix(n.Int64, 0).Format(time.RFC3339)
	}

	switch fs.Arg(0) {
	case "create":
		createCmd := flag.NewFlagSet("tokens create", flag.ExitOnError)
		userEmail := createCmd.String("user", "", "user email")
		name := createCmd.String("name", "", "token name")
		if err := createCmd.Parse(fs.Args()[1:]); err != nil {
			return 1
		}
		if *userEmail == "" {
			fmt.Fprintln(os.Stderr, "error: --user is required")
			return 1
		}
		if *name == "" {
			fmt.Fprintln(os.Stderr, "error: --name is required")
			return 1
		}

		user, err := repo.Queries.GetUserByEmail(ctx, *userEmail)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				fmt.Fprintf(
					os.Stderr,
					"error: user not found: %s\n",
					*userEmail,
				)
				return 1
			}
			fmt.Fprintf(os.Stderr, "error: lookup user: %v\n", err)
			return 1
		}

		full, prefix, hash, err := auth.MintApiToken()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: mint token: %v\n", err)
			return 1
		}

		if _, err := repo.Queries.CreateApiToken(ctx, db.CreateApiTokenParams{
			ID:          uuid.NewString(),
			UserID:      user.ID,
			Name:        *name,
			TokenPrefix: prefix,
			TokenHash:   hash,
			Scope:       "global",
			ExpiresAt:   sql.NullInt64{Valid: false},
		}); err != nil {
			fmt.Fprintf(os.Stderr, "error: create token: %v\n", err)
			return 1
		}

		fmt.Println(full)
		fmt.Fprintln(
			os.Stderr,
			"warning: this is the only time the plaintext token is shown",
		)
		return 0

	case "list":
		listCmd := flag.NewFlagSet("tokens list", flag.ExitOnError)
		userEmail := listCmd.String("user", "", "user email")
		if err := listCmd.Parse(fs.Args()[1:]); err != nil {
			return 1
		}

		if *userEmail != "" {
			user, err := repo.Queries.GetUserByEmail(ctx, *userEmail)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					fmt.Fprintf(
						os.Stderr,
						"error: user not found: %s\n",
						*userEmail,
					)
					return 1
				}
				fmt.Fprintf(os.Stderr, "error: lookup user: %v\n", err)
				return 1
			}
			rows, err := repo.Queries.ListApiTokensByUser(ctx, user.ID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: list tokens: %v\n", err)
				return 1
			}
			fmt.Println(
				"PREFIX\tNAME\tCREATED_AT\tLAST_USED_AT\tEXPIRES_AT\tREVOKED_AT",
			)
			for _, r := range rows {
				fmt.Printf(
					"%s\t%s\t%s\t%s\t%s\t%s\n",
					r.TokenPrefix,
					r.Name,
					fmtTime(r.CreatedAt),
					fmtNullTime(r.LastUsedAt),
					fmtNullTime(r.ExpiresAt),
					fmtNullTime(r.RevokedAt),
				)
			}
			return 0
		}

		rows, err := repo.Queries.ListAllApiTokens(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: list tokens: %v\n", err)
			return 1
		}
		fmt.Println(
			"PREFIX\tNAME\tUSER_EMAIL\tCREATED_AT\tLAST_USED_AT\tEXPIRES_AT\tREVOKED_AT",
		)
		for _, r := range rows {
			fmt.Printf(
				"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				r.TokenPrefix,
				r.Name,
				r.Email,
				fmtTime(r.CreatedAt),
				fmtNullTime(r.LastUsedAt),
				fmtNullTime(r.ExpiresAt),
				fmtNullTime(r.RevokedAt),
			)
		}
		return 0

	case "revoke":
		if fs.NArg() < 2 {
			fmt.Fprintln(
				os.Stderr,
				"Usage: durpdeploy tokens revoke <token_prefix>",
			)
			return 1
		}
		prefix := fs.Arg(1)

		rows, err := repo.Queries.ListAllApiTokens(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: list tokens: %v\n", err)
			return 1
		}
		var targetID string
		for _, r := range rows {
			if r.TokenPrefix == prefix && !r.RevokedAt.Valid {
				targetID = r.ID
				break
			}
		}
		if targetID == "" {
			fmt.Fprintf(
				os.Stderr,
				"error: no active token with prefix %q\n",
				prefix,
			)
			return 1
		}
		if err := repo.Queries.RevokeApiToken(ctx, targetID); err != nil {
			fmt.Fprintf(os.Stderr, "error: revoke token: %v\n", err)
			return 1
		}
		fmt.Printf("Revoked token with prefix %s\n", prefix)
		return 0

	default:
		fmt.Fprintf(
			os.Stderr,
			"unknown tokens subcommand %q; supported: create, list, revoke\n",
			fs.Arg(0),
		)
		return 1
	}
}
