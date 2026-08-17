//go:build oidctest

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/oidc"
	"durpdeploy/internal/oidc/oidctest"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"

	"github.com/robfig/cron/v3"
)

type readinessState struct {
	AppURL string `json:"app_url"`
	IDPURL string `json:"idp_url"`
	PID    int    `json:"pid"`
}

func runHarness(ctx context.Context, config commandConfig) (err error) {
	if err := removeReadiness(config.readyFile); err != nil {
		return err
	}
	workDir, err := os.MkdirTemp("", "durpdeploy-oidc-browser-")
	if err != nil {
		return fmt.Errorf("create harness work directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen on loopback: %w", err)
	}
	defer listener.Close()
	callbackURL := "http://" + listener.Addr().String() +
		"/login/oidc/callback"

	fixture, err := oidctest.New(oidctest.Options{CallbackURL: callbackURL})
	if err != nil {
		return fmt.Errorf("start TLS OIDC fixture: %w", err)
	}
	fixtureOpen := true
	defer func() {
		if fixtureOpen {
			fixture.Close()
		}
	}()
	if err := verifyTrustedOIDC(ctx, fixture); err != nil {
		return err
	}

	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		filepath.Join(workDir, "fixture.db"),
	)
	dbConn, err := migrate.Run(dsn)
	if err != nil {
		return fmt.Errorf("migrate harness database: %w", err)
	}
	defer func() {
		closeErr := dbConn.Close()
		if err == nil && closeErr != nil {
			err = fmt.Errorf("close harness database: %w", closeErr)
		}
	}()

	repo := repository.New(dbConn)
	passwordHash, err := auth.HashPassword("browser-fallback-password")
	if err != nil {
		return fmt.Errorf("hash browser fallback password: %w", err)
	}
	if _, err := repo.Queries.CreateUser(ctx, db.CreateUserParams{
		Email:        "fixture@example.test",
		PasswordHash: passwordHash,
		Name:         "Browser Fallback",
		Role:         "admin",
	}); err != nil {
		return fmt.Errorf("create browser fallback user: %w", err)
	}
	deploymentRunner := runner.New(repo, runner.NewLogBroker())
	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	authHandler := handler.NewAuthHandler(repo)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		return fmt.Errorf("create OIDC transaction secret box: %w", err)
	}
	codec, err := oidc.NewTransactionCookieCodec(
		box,
		oidc.TransactionCookieConfig{Secure: false, Now: fixture.Now},
	)
	if err != nil {
		return fmt.Errorf("create OIDC transaction cookie codec: %w", err)
	}
	transactions, err := oidc.NewTransactionStore(oidc.TransactionStoreOptions{
		Repository:  repo,
		CookieCodec: codec,
	})
	if err != nil {
		return fmt.Errorf("create OIDC transaction store: %w", err)
	}
	provider, err := oidc.NewProvider(oidc.ProviderOptions{
		Config: oidc.Config{
			Enabled:       true,
			Issuer:        fixture.URL(),
			ClientID:      fixture.ClientID(),
			ClientSecret:  fixture.ClientSecret(),
			CallbackURL:   callbackURL,
			Scopes:        []string{"openid", "profile", "email"},
			GroupClaim:    "groups",
			AdminGroup:    "fixture-admin",
			DeployerGroup: "fixture-deployer",
			ViewerGroup:   "fixture-viewer",
		},
		HTTPClient: fixture.Client(),
		Now:        fixture.Now,
	})
	if err != nil {
		return fmt.Errorf("create OIDC provider: %w", err)
	}
	authHandler.SetOIDCDisplayName("Fixture SSO")
	authHandler.SetOIDCLogin(provider, transactions)
	app := &http.Server{
		Handler: server.NewRouter(
			repo,
			deploymentRunner,
			parser,
			authHandler,
			true,
		),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- app.Serve(listener)
	}()
	defer removeReadiness(config.readyFile)
	if config.outage {
		fixture.Close()
		fixtureOpen = false
	}

	state := readinessState{
		AppURL: "http://" + listener.Addr().String(),
		IDPURL: fixture.URL(),
		PID:    os.Getpid(),
	}
	if err := writeReadiness(config.readyFile, state); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cancel()
		if shutdownErr := app.Shutdown(shutdownCtx); shutdownErr != nil {
			return fmt.Errorf("shutdown browser harness: %w", shutdownErr)
		}
		serveErr := <-serveErr
		if !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("wait for browser harness: %w", serveErr)
		}
		return nil
	case serveErr := <-serveErr:
		if !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("serve browser harness: %w", serveErr)
		}
		return nil
	}
}
